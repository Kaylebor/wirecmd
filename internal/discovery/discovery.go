// Package discovery resolves trusted Wirecmd configuration sources.
package discovery

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"syscall"
)

const (
	projectConfig = "wirecmd.kdl"
	stateVersion  = 1
)

// UntrustedError reports the nearest workspace configuration that requires trust.
type UntrustedError struct{ Workspace string }

func (e *UntrustedError) Error() string {
	return fmt.Sprintf("workspace configuration under %q is not trusted", e.Workspace)
}

// NotFoundError reports that neither global nor workspace configuration exists.
type NotFoundError struct{}

func (*NotFoundError) Error() string { return "no Wirecmd configuration was found" }

type state struct {
	Version    int      `json:"version"`
	Workspaces []string `json:"workspaces"`
}

// Paths returns configuration paths from weakest to strongest for cwd.
func Paths(cwd string) ([]string, error) {
	cwd, err := canonicalDirectory(cwd)
	if err != nil {
		return nil, err
	}
	trusted, err := List()
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0)
	global, err := globalConfigPath()
	if err != nil {
		return nil, err
	}
	if exists, err := pathExists(global); err != nil {
		return nil, err
	} else if exists {
		paths = append(paths, global)
	}

	root := nearestTrustedAncestor(cwd, trusted)
	if root == "" {
		if workspace, err := nearestProjectConfig(cwd); err != nil {
			return nil, err
		} else if workspace != "" {
			return nil, &UntrustedError{Workspace: workspace}
		}
		if len(paths) == 0 {
			return nil, &NotFoundError{}
		}
		return paths, nil
	}

	dirs := []string{}
	for dir := cwd; ; dir = filepath.Dir(dir) {
		dirs = append(dirs, dir)
		if dir == root {
			break
		}
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		candidate := filepath.Join(dirs[i], projectConfig)
		info, err := os.Lstat(candidate)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("inspect discovered config %q: %w", candidate, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("discovered config %q must be a regular non-symlink file", candidate)
		}
		paths = append(paths, candidate)
	}
	if len(paths) == 0 {
		return nil, &NotFoundError{}
	}
	return paths, nil
}

// Trust adds path to the trusted workspace roots and returns its canonical form.
func Trust(path string) (string, error) {
	path, err := canonicalDirectory(path)
	if err != nil {
		return "", err
	}
	err = update(func(s *state) {
		for _, existing := range s.Workspaces {
			if existing == path {
				return
			}
		}
		s.Workspaces = append(s.Workspaces, path)
		sort.Strings(s.Workspaces)
	})
	return path, err
}

// Untrust removes path from the trusted workspace roots and returns its canonical form.
func Untrust(path string) (string, error) {
	path, err := canonicalRemovalPath(path)
	if err != nil {
		return "", err
	}
	err = update(func(s *state) {
		for i, existing := range s.Workspaces {
			if existing == path {
				s.Workspaces = append(s.Workspaces[:i], s.Workspaces[i+1:]...)
				return
			}
		}
	})
	return path, err
}

// Status returns the canonical path, whether it is covered, and the nearest trusted root.
func Status(path string) (string, bool, string, error) {
	path, err := canonicalDirectory(path)
	if err != nil {
		return "", false, "", err
	}
	trusted, err := List()
	if err != nil {
		return "", false, "", err
	}
	root := nearestTrustedAncestor(path, trusted)
	return path, root != "", root, nil
}

// List returns canonical trusted workspace roots in deterministic order.
func List() ([]string, error) {
	dir, err := stateDirectory()
	if err != nil {
		return nil, err
	}
	if err := validateStateDirectory(dir, false); err != nil {
		return nil, err
	}
	s, err := readState(filepath.Join(dir, "trust.json"))
	if err != nil {
		return nil, err
	}
	return append([]string(nil), s.Workspaces...), nil
}

func update(change func(*state)) error {
	dir, err := stateDirectory()
	if err != nil {
		return err
	}
	if err := validateStateDirectory(dir, true); err != nil {
		return err
	}
	lockPath := filepath.Join(dir, "trust.lock")
	if info, err := os.Lstat(lockPath); err == nil && (info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || !owned(info)) {
		return fmt.Errorf("unsafe trust lock %q", lockPath)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := lock.Chmod(0o600); err != nil {
		return err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) //nolint:errcheck

	path := filepath.Join(dir, "trust.json")
	s, err := readState(path)
	if err != nil {
		return err
	}
	change(&s)
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	temp, err := os.CreateTemp(dir, ".trust-*")
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
	return os.Rename(tempPath, path)
}

func readState(path string) (state, error) {
	s := state{Version: stateVersion, Workspaces: []string{}}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || !owned(info) {
		return s, fmt.Errorf("unsafe trust state %q", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return s, fmt.Errorf("decode trust state: %w", err)
	}
	if s.Version != stateVersion {
		return s, fmt.Errorf("unsupported trust state version %d", s.Version)
	}
	seen := make(map[string]struct{}, len(s.Workspaces))
	for _, workspace := range s.Workspaces {
		if !filepath.IsAbs(workspace) || filepath.Clean(workspace) != workspace {
			return s, fmt.Errorf("invalid workspace path %q in trust state", workspace)
		}
		if _, duplicate := seen[workspace]; duplicate {
			return s, fmt.Errorf("duplicate workspace path %q in trust state", workspace)
		}
		seen[workspace] = struct{}{}
	}
	sort.Strings(s.Workspaces)
	return s, nil
}

func validateStateDirectory(path string, create bool) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) && !create {
		return nil
	}
	if errors.Is(err, os.ErrNotExist) {
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
		return fmt.Errorf("unsafe trust state directory %q", path)
	}
	return nil
}

func stateDirectory() (string, error) {
	if path := os.Getenv("XDG_STATE_HOME"); filepath.IsAbs(path) {
		return filepath.Join(path, "wirecmd"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "wirecmd"), nil
}

func globalConfigPath() (string, error) {
	if path := os.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(path) {
		return filepath.Join(path, "wirecmd", "config.kdl"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "wirecmd", "config.kdl"), nil
}

func canonicalDirectory(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%q is not a directory", path)
	}
	return filepath.Clean(resolved), nil
}

func canonicalRemovalPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if errors.Is(err, os.ErrNotExist) {
		current := abs
		var missing []string
		for {
			parent := filepath.Dir(current)
			if parent == current {
				return filepath.Clean(abs), nil
			}
			missing = append(missing, filepath.Base(current))
			current = parent
			resolved, err = filepath.EvalSymlinks(current)
			if err == nil {
				for i := len(missing) - 1; i >= 0; i-- {
					resolved = filepath.Join(resolved, missing[i])
				}
				return filepath.Clean(resolved), nil
			}
			if !errors.Is(err, os.ErrNotExist) {
				return "", err
			}
		}
	}
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%q is not a directory", path)
	}
	return filepath.Clean(resolved), nil
}

func nearestTrustedAncestor(path string, trusted []string) string {
	best := ""
	for _, root := range trusted {
		if within(root, path) && len(root) > len(best) {
			best = root
		}
	}
	return best
}

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !filepath.IsAbs(rel) && (rel == "." || (len(rel) > 2 && rel[:3] != ".."+string(filepath.Separator)))
}

func nearestProjectConfig(cwd string) (string, error) {
	for dir := cwd; ; dir = filepath.Dir(dir) {
		candidate := filepath.Join(dir, projectConfig)
		if _, err := os.Lstat(candidate); err == nil {
			return dir, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", nil
		}
	}
}

func pathExists(path string) (bool, error) {
	_, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

func owned(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Geteuid())
}
