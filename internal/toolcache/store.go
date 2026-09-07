// Package toolcache persists normalized MCP tool metadata for offline help.
// It deliberately has no dependency on MCP SDK types or caller identity shape.
package toolcache

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"unicode"
)

const (
	stateVersion = 1
	metadataDir  = "toolcache"
	metadataLock = "metadata.lock"
)

var (
	ErrMiss        = errors.New("tool metadata cache miss")
	ErrCorrupt     = errors.New("tool metadata cache record is corrupt")
	ErrUnsafeState = errors.New("unsafe tool metadata cache state")
	ErrInvalid     = errors.New("invalid tool metadata cache input")
)

// Options controls Store dependencies. StateDir is Wirecmd's private state
// directory; the cache owns a toolcache child directory beneath it.
type Options struct{ StateDir string }

// Store serializes exact-identity catalog access. The caller supplies opaque
// identities; identities are never persisted except as SHA-256 filenames.
type Store struct{ stateDir string }

// Catalog is normalized metadata for server and focused tool help. Complete
// means Tools is a complete successful server listing.
type Catalog struct {
	Server   string `json:"server"`
	Complete bool   `json:"complete"`
	Tools    []Tool `json:"tools"`
}

// Tool is terminal-safe, already-redacted metadata. Detailed distinguishes a
// targeted schema discovery (including a legitimately empty schema) from a
// summary-only listing.
type Tool struct {
	Name         string          `json:"name"`
	Title        string          `json:"title,omitempty"`
	Description  string          `json:"description,omitempty"`
	InputSchema  json.RawMessage `json:"input_schema,omitempty"`
	OutputSchema json.RawMessage `json:"output_schema,omitempty"`
	Detailed     bool            `json:"detailed"`
}

type diskRecord struct {
	Version int     `json:"version"`
	Catalog Catalog `json:"catalog"`
}

// New opens the default XDG state location without accessing the filesystem.
func New() (*Store, error) { return Open(Options{}) }

func Open(options Options) (*Store, error) {
	stateDir := options.StateDir
	if stateDir == "" {
		var err error
		stateDir, err = defaultStateDir()
		if err != nil {
			return nil, err
		}
	}
	if !filepath.IsAbs(stateDir) {
		return nil, fmt.Errorf("%w: state directory must be absolute", ErrUnsafeState)
	}
	return &Store{stateDir: filepath.Clean(stateDir)}, nil
}

// Load returns only an exact identity match. ErrMiss identifies absent state;
// malformed/unsupported and unsafe state retain distinct typed errors.
func (s *Store) Load(identity string) (Catalog, error) {
	if err := validIdentity(identity); err != nil {
		return Catalog{}, err
	}
	dir, err := s.cacheDirectory(false)
	if err != nil {
		return Catalog{}, err
	}
	var catalog Catalog
	err = s.withLock(dir, func() error {
		data, found, err := readPrivateFile(s.recordPath(identity))
		if err != nil {
			return err
		}
		if !found {
			return ErrMiss
		}
		catalog, err = decodeRecord(data)
		return err
	})
	if err != nil {
		return Catalog{}, err
	}
	return catalog, nil
}

// Replace stores a complete successful catalog, removing disappeared tools.
func (s *Store) Replace(identity string, catalog Catalog) error {
	if err := validIdentity(identity); err != nil {
		return err
	}
	if !catalog.Complete {
		return fmt.Errorf("%w: replacement catalog must be complete", ErrInvalid)
	}
	normalized, err := normalizeCatalog(catalog)
	if err != nil {
		return err
	}
	dir, err := s.cacheDirectory(true)
	if err != nil {
		return err
	}
	return s.withLock(dir, func() error {
		return writePrivateFile(s.recordPath(identity), encodeRecord(normalized))
	})
}

// Merge adds or updates a targeted tool. Supplied non-empty fields replace
// stale values; absent fields preserve prior detail. It never changes a
// catalog's completeness or clears an existing detailed discovery marker.
func (s *Store) Merge(identity, server string, tool Tool) error {
	return s.merge(identity, server, tool, false)
}

// MergeExact replaces a targeted tool record with the complete discovered
// result, clearing stale fields absent from that result. It preserves only the
// catalog's Complete flag and other tool records.
func (s *Store) MergeExact(identity, server string, tool Tool) error {
	return s.merge(identity, server, tool, true)
}

func (s *Store) merge(identity, server string, tool Tool, exact bool) error {
	if err := validIdentity(identity); err != nil {
		return err
	}
	if err := validText("server", server); err != nil {
		return err
	}
	normalizedTool, err := normalizeTool(tool)
	if err != nil {
		return err
	}
	return s.write(identity, func(catalog Catalog, found bool) (Catalog, error) {
		if !found {
			catalog = Catalog{Server: server}
		} else if catalog.Server != server {
			return Catalog{}, fmt.Errorf("%w: cached server does not match targeted discovery", ErrInvalid)
		}
		for i := range catalog.Tools {
			if catalog.Tools[i].Name == normalizedTool.Name {
				if exact {
					catalog.Tools[i] = normalizedTool
				} else {
					catalog.Tools[i] = mergeTool(catalog.Tools[i], normalizedTool)
				}
				return normalizeCatalog(catalog)
			}
		}
		catalog.Tools = append(catalog.Tools, normalizedTool)
		return normalizeCatalog(catalog)
	})
}

func mergeTool(existing, update Tool) Tool {
	if update.Title != "" {
		existing.Title = update.Title
	}
	if update.Description != "" {
		existing.Description = update.Description
	}
	if len(update.InputSchema) != 0 {
		existing.InputSchema = update.InputSchema
	}
	if len(update.OutputSchema) != 0 {
		existing.OutputSchema = update.OutputSchema
	}
	existing.Detailed = existing.Detailed || update.Detailed
	return existing
}

func (s *Store) write(identity string, update func(Catalog, bool) (Catalog, error)) error {
	dir, err := s.cacheDirectory(true)
	if err != nil {
		return err
	}
	return s.withLock(dir, func() error {
		path := s.recordPath(identity)
		data, found, err := readPrivateFile(path)
		if err != nil {
			return err
		}
		var catalog Catalog
		if found {
			catalog, err = decodeRecord(data)
			if err != nil {
				return err
			}
		}
		catalog, err = update(catalog, found)
		if err != nil {
			return err
		}
		return writePrivateFile(path, encodeRecord(catalog))
	})
}

func (s *Store) recordPath(identity string) string {
	sum := sha256.Sum256([]byte(identity))
	return filepath.Join(s.stateDir, metadataDir, hex.EncodeToString(sum[:])+".json")
}

func (s *Store) withLock(dir string, operation func() error) error {
	lock, err := openPrivateLock(filepath.Join(dir, metadataLock))
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("lock tool metadata cache: %w", err)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) //nolint:errcheck
	return operation()
}

func (s *Store) cacheDirectory(create bool) (string, error) {
	if err := privateDirectory(s.stateDir, create); err != nil {
		return "", err
	}
	dir := filepath.Join(s.stateDir, metadataDir)
	if err := privateDirectory(dir, create); err != nil {
		return "", err
	}
	return dir, nil
}

func defaultStateDir() (string, error) {
	if stateHome := os.Getenv("XDG_STATE_HOME"); filepath.IsAbs(stateHome) {
		return filepath.Join(stateHome, "wirecmd"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "wirecmd"), nil
}

func privateDirectory(path string, create bool) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if !create {
			return ErrMiss
		}
		if err := os.MkdirAll(path, 0o700); err != nil {
			return err
		}
		if err := os.Chmod(path, 0o700); err != nil {
			return err
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || info.Mode().Perm() != 0o700 || !owned(info) {
		return fmt.Errorf("%w: directory %q", ErrUnsafeState, path)
	}
	return nil
}

func openPrivateLock(path string) (*os.File, error) {
	if info, err := os.Lstat(path); err == nil {
		if err := validatePrivateFile(path, info); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	fd, err := syscall.Open(path, syscall.O_RDWR|syscall.O_CREAT|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open tool metadata lock: %w", err)
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = syscall.Close(fd)
		return nil, fmt.Errorf("open tool metadata lock: invalid file descriptor")
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if err := validatePrivateFile(path, info); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func readPrivateFile(path string) ([]byte, bool, error) {
	pathInfo, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if err := validatePrivateFile(path, pathInfo); err != nil {
		return nil, false, err
	}
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, false, fmt.Errorf("open tool metadata record: %w", err)
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = syscall.Close(fd)
		return nil, false, fmt.Errorf("open tool metadata record: invalid file descriptor")
	}
	defer file.Close()
	openInfo, err := file.Stat()
	if err != nil {
		return nil, false, err
	}
	if err := validatePrivateFile(path, openInfo); err != nil || !os.SameFile(pathInfo, openInfo) {
		if err == nil {
			err = fmt.Errorf("%w: record %q changed while opening", ErrUnsafeState, path)
		}
		return nil, false, err
	}
	data, err := io.ReadAll(file)
	return data, true, err
}

func writePrivateFile(path string, data []byte) error {
	if info, err := os.Lstat(path); err == nil {
		if err := validatePrivateFile(path, info); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, ".toolcache-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err == nil {
		_, err = temp.Write(data)
	}
	if err == nil {
		err = temp.Sync()
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil {
		if err := validatePrivateFile(path, info); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err != nil {
		return err
	} else if err := validatePrivateFile(path, info); err != nil {
		return err
	}
	return syncDirectory(dir)
}

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func decodeRecord(data []byte) (Catalog, error) {
	var record diskRecord
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil || requireEOF(decoder) != nil {
		return Catalog{}, fmt.Errorf("%w: decode record", ErrCorrupt)
	}
	if record.Version != stateVersion {
		return Catalog{}, fmt.Errorf("%w: unsupported record version %d", ErrCorrupt, record.Version)
	}
	catalog, err := normalizeCatalog(record.Catalog)
	if err != nil {
		return Catalog{}, fmt.Errorf("%w: invalid catalog: %v", ErrCorrupt, err)
	}
	return catalog, nil
}

func requireEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func encodeRecord(catalog Catalog) []byte {
	data, err := json.Marshal(diskRecord{Version: stateVersion, Catalog: catalog})
	if err != nil {
		panic("validated tool metadata catalog failed to encode: " + err.Error())
	}
	return append(data, '\n')
}

func normalizeCatalog(catalog Catalog) (Catalog, error) {
	if err := validText("server", catalog.Server); err != nil {
		return Catalog{}, err
	}
	normalized := Catalog{Server: catalog.Server, Complete: catalog.Complete, Tools: make([]Tool, 0, len(catalog.Tools))}
	seen := make(map[string]struct{}, len(catalog.Tools))
	for _, tool := range catalog.Tools {
		tool, err := normalizeTool(tool)
		if err != nil {
			return Catalog{}, err
		}
		if _, exists := seen[tool.Name]; exists {
			return Catalog{}, fmt.Errorf("%w: duplicate tool name %q", ErrInvalid, tool.Name)
		}
		seen[tool.Name] = struct{}{}
		normalized.Tools = append(normalized.Tools, tool)
	}
	sort.Slice(normalized.Tools, func(i, j int) bool { return normalized.Tools[i].Name < normalized.Tools[j].Name })
	return normalized, nil
}

func normalizeTool(tool Tool) (Tool, error) {
	for _, field := range []struct{ name, value string }{{"tool name", tool.Name}, {"tool title", tool.Title}, {"tool description", tool.Description}} {
		if err := validText(field.name, field.value); err != nil {
			return Tool{}, err
		}
	}
	input, err := normalizeJSON(tool.InputSchema)
	if err != nil {
		return Tool{}, fmt.Errorf("%w: input schema: %v", ErrInvalid, err)
	}
	output, err := normalizeJSON(tool.OutputSchema)
	if err != nil {
		return Tool{}, fmt.Errorf("%w: output schema: %v", ErrInvalid, err)
	}
	tool.InputSchema, tool.OutputSchema = input, output
	return tool, nil
}

func normalizeJSON(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil || requireEOF(decoder) != nil {
		return nil, errors.New("must be one valid JSON value")
	}
	normalized, err := json.Marshal(value)
	return json.RawMessage(normalized), err
}

func validIdentity(identity string) error {
	if identity == "" {
		return fmt.Errorf("%w: identity must not be empty", ErrInvalid)
	}
	return nil
}

func validText(field, value string) error {
	allowDescriptionLayout := field == "tool description"
	if value == "" && (field == "tool title" || allowDescriptionLayout) {
		return nil
	}
	if value == "" {
		return fmt.Errorf("%w: %s must not be empty", ErrInvalid, field)
	}
	if strings.IndexFunc(value, func(r rune) bool {
		return bidiControl(r) || (unicode.IsControl(r) && (!allowDescriptionLayout || (r != '\n' && r != '\t')))
	}) >= 0 {
		return fmt.Errorf("%w: %s contains terminal control characters", ErrInvalid, field)
	}
	return nil
}

func bidiControl(r rune) bool {
	return r == 0x061C || r == 0x200E || r == 0x200F || (r >= 0x202A && r <= 0x202E) || (r >= 0x2066 && r <= 0x2069)
}

func validatePrivateFile(path string, info os.FileInfo) error {
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || !owned(info) {
		return fmt.Errorf("%w: file %q", ErrUnsafeState, path)
	}
	return nil
}

func owned(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(stat.Uid) == os.Geteuid()
}
