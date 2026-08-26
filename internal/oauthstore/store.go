// Package oauthstore persists opaque OAuth state without placing credentials in
// the filesystem in plaintext. The native keyring contains only a random
// master key; every record is independently authenticated and encrypted.
package oauthstore

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	keyring "github.com/zalando/go-keyring"
)

const (
	stateVersion     = 1
	keyringService   = "wirecmd"
	keyringAccount   = "oauth-master-key-v1"
	credentialsDir   = "oauth"
	credentialsLock  = "oauth.lock"
	masterKeySize    = 32
	generationLength = 16
)

var (
	// ErrUnavailable means the native credential service could not be used.
	// Callers should present a user action rather than silently persisting
	// credentials elsewhere.
	ErrUnavailable = errors.New("OAuth credential store unavailable")
	// ErrCorrupt means a record was present but cannot safely be decoded or
	// authenticated with the current master key.
	ErrCorrupt = errors.New("OAuth credential store record is corrupt")
	// ErrUnsafeState means a required private state path is a symlink, is not
	// owned by this user, or has insecure permissions.
	ErrUnsafeState = errors.New("unsafe OAuth credential state")
	// ErrStale means a caller tried to refresh a credential generation that
	// has since been replaced or deleted.
	ErrStale = errors.New("OAuth credential generation is stale")
	// ErrAuthorizationLocked means another process is already completing an
	// authorization flow for the same opaque credential identity.
	ErrAuthorizationLocked = errors.New("OAuth authorization is already in progress")
)

// Keyring is the small subset of the native keyring used by Store. Supplying
// one through Options makes persistence tests independent of the host desktop
// keyring.
type Keyring interface {
	Get(service, account string) (string, error)
	Set(service, account, secret string) error
	Delete(service, account string) error
}

type nativeKeyring struct{}

func (nativeKeyring) Get(service, account string) (string, error) {
	return keyring.Get(service, account)
}

func (nativeKeyring) Set(service, account, secret string) error {
	return keyring.Set(service, account, secret)
}

func (nativeKeyring) Delete(service, account string) error {
	return keyring.Delete(service, account)
}

// Options controls Store dependencies. StateDir is the private Wirecmd state
// directory (not the OAuth subdirectory); the default follows XDG_STATE_HOME
// and then ~/.local/state. Random defaults to crypto/rand.Reader.
type Options struct {
	StateDir string
	Keyring  Keyring
	Random   io.Reader
}

// Store provides serialized encrypted records. The identity passed to methods
// must be a stable, canonical byte encoding of the caller's credential scope.
// It is never used as a pathname or written to disk.
type Store struct {
	stateDir string
	keyring  Keyring
	random   io.Reader
}

// Record is an encrypted OAuth state payload. Generation is stable across
// saves when supplied by the caller; Save creates one for a new record.
// Payload is deliberately SDK-agnostic JSON owned by the OAuth execution
// layer, which prevents this package from becoming an OAuth protocol shim.
type Record struct {
	Generation string          `json:"generation"`
	Payload    json.RawMessage `json:"payload"`
}

type encryptedRecord struct {
	Version    int    `json:"version"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

// New opens a store using the standard Wirecmd state location. It performs no
// keyring or filesystem access until an operation is requested.
func New() (*Store, error) {
	return Open(Options{})
}

// Open constructs a Store with optional test dependencies.
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
	if options.Keyring == nil {
		options.Keyring = nativeKeyring{}
	}
	if options.Random == nil {
		options.Random = rand.Reader
	}
	return &Store{stateDir: filepath.Clean(stateDir), keyring: options.Keyring, random: options.Random}, nil
}

// Load retrieves one record. found is false only when no matching credentials
// are stored; unavailable, unsafe, and corrupted states always return an
// error so callers never mistake them for logged-out state.
func (s *Store) Load(identity []byte) (record Record, found bool, err error) {
	if err := validIdentity(identity); err != nil {
		return Record{}, false, err
	}
	key, exists, err := s.masterKey(false)
	if err != nil || !exists {
		return Record{}, false, err
	}
	if err := s.withLock(func() error {
		id := opaqueID(key, identity)
		data, exists, err := readPrivateFile(s.recordPath(id))
		if err != nil {
			return err
		}
		if !exists {
			return nil
		}
		record, err = decryptRecord(key, id, data)
		if err != nil {
			return err
		}
		found = true
		return nil
	}); err != nil {
		return Record{}, false, err
	}
	return record, found, nil
}

// Save encrypts a record and returns its resulting generation. Calling Save
// with an existing generation preserves that generation; an empty generation
// creates a fresh opaque generation for a newly authenticated record.
func (s *Store) Save(identity []byte, record Record) (Record, error) {
	if err := validIdentity(identity); err != nil {
		return Record{}, err
	}
	if !json.Valid(record.Payload) {
		return Record{}, fmt.Errorf("OAuth credential payload must be valid JSON")
	}
	replace := record.Generation == ""
	if replace {
		generation, err := randomText(s.random, generationLength)
		if err != nil {
			return Record{}, fmt.Errorf("generate OAuth credential generation: %w", err)
		}
		record.Generation = generation
	}
	if err := s.withLock(func() error {
		key, _, err := s.masterKey(true)
		if err != nil {
			return err
		}
		id := opaqueID(key, identity)
		if !replace {
			data, exists, err := readPrivateFile(s.recordPath(id))
			if err != nil {
				return err
			}
			if !exists {
				return ErrStale
			}
			current, err := decryptRecord(key, id, data)
			if err != nil {
				return err
			}
			if current.Generation != record.Generation {
				return ErrStale
			}
		}
		data, err := encryptRecord(key, id, record, s.random)
		if err != nil {
			return err
		}
		return writePrivateFile(s.recordPath(id), data)
	}); err != nil {
		return Record{}, err
	}
	return record, nil
}

// Delete removes a stored record. found is false when no matching record is
// present, including when Wirecmd has never created a master key.
func (s *Store) Delete(identity []byte) (found bool, err error) {
	if err := validIdentity(identity); err != nil {
		return false, err
	}
	key, exists, err := s.masterKey(false)
	if err != nil || !exists {
		return false, err
	}
	err = s.withLock(func() error {
		id := opaqueID(key, identity)
		return removePrivateFile(s.recordPath(id), &found)
	})
	return found, err
}

// LockAuthorization acquires a non-blocking, cross-process lease for one
// credential identity. The returned release function must be called when the
// browser flow ends. The opaque lock filename reveals neither the endpoint nor
// registration inputs.
func (s *Store) LockAuthorization(identity []byte) (release func(), err error) {
	if err := validIdentity(identity); err != nil {
		return nil, err
	}
	var id string
	if err := s.withLock(func() error {
		key, _, err := s.masterKey(true)
		if err != nil {
			return err
		}
		id = opaqueID(key, identity)
		return nil
	}); err != nil {
		return nil, err
	}
	dir, err := s.credentialsDirectory()
	if err != nil {
		return nil, err
	}
	lock, err := openPrivateLock(filepath.Join(dir, id+".auth.lock"))
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = lock.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, ErrAuthorizationLocked
		}
		return nil, fmt.Errorf("lock OAuth authorization state: %w", err)
	}
	return func() {
		_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
		_ = lock.Close()
	}, nil
}

func (s *Store) recordPath(id string) string {
	return filepath.Join(s.stateDir, credentialsDir, id+".enc")
}

func (s *Store) withLock(operation func() error) error {
	dir, err := s.credentialsDirectory()
	if err != nil {
		return err
	}
	lock, err := openPrivateLock(filepath.Join(dir, credentialsLock))
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("lock OAuth credential state: %w", err)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) //nolint:errcheck
	return operation()
}

func (s *Store) credentialsDirectory() (string, error) {
	if err := ensurePrivateDir(s.stateDir); err != nil {
		return "", err
	}
	dir := filepath.Join(s.stateDir, credentialsDir)
	if err := ensurePrivateDir(dir); err != nil {
		return "", err
	}
	return dir, nil
}

func (s *Store) masterKey(create bool) ([]byte, bool, error) {
	encoded, err := s.keyring.Get(keyringService, keyringAccount)
	if errors.Is(err, keyring.ErrNotFound) {
		if !create {
			return nil, false, nil
		}
		key := make([]byte, masterKeySize)
		if _, err := io.ReadFull(s.random, key); err != nil {
			return nil, false, fmt.Errorf("generate OAuth credential key: %w", err)
		}
		encoded := base64.RawStdEncoding.EncodeToString(key)
		if err := s.keyring.Set(keyringService, keyringAccount, encoded); err != nil {
			return nil, false, keyringFailure("store master key", err)
		}
		return key, true, nil
	}
	if err != nil {
		return nil, false, keyringFailure("load master key", err)
	}
	key, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil || len(key) != masterKeySize {
		return nil, false, fmt.Errorf("%w: invalid master key", ErrCorrupt)
	}
	return key, true, nil
}

func keyringFailure(operation string, err error) error {
	return fmt.Errorf("%w: %s: %v", ErrUnavailable, operation, err)
}

func validIdentity(identity []byte) error {
	if len(identity) == 0 {
		return fmt.Errorf("OAuth credential identity must not be empty")
	}
	return nil
}

func opaqueID(key, identity []byte) string {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte("wirecmd.oauth.record.v1\x00"))
	_, _ = mac.Write(identity)
	return hex.EncodeToString(mac.Sum(nil))
}

func encryptRecord(key []byte, id string, record Record, random io.Reader) ([]byte, error) {
	plaintext, err := json.Marshal(record)
	if err != nil {
		return nil, fmt.Errorf("encode OAuth credential record: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(random, nonce); err != nil {
		return nil, fmt.Errorf("generate OAuth credential nonce: %w", err)
	}
	envelope := encryptedRecord{
		Version:    stateVersion,
		Nonce:      base64.RawStdEncoding.EncodeToString(nonce),
		Ciphertext: base64.RawStdEncoding.EncodeToString(aead.Seal(nil, nonce, plaintext, []byte(id))),
	}
	data, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("encode encrypted OAuth credential record: %w", err)
	}
	return append(data, '\n'), nil
}

func decryptRecord(key []byte, id string, data []byte) (Record, error) {
	var envelope encryptedRecord
	if err := json.Unmarshal(data, &envelope); err != nil {
		return Record{}, fmt.Errorf("%w: decode encrypted record", ErrCorrupt)
	}
	if envelope.Version != stateVersion {
		return Record{}, fmt.Errorf("%w: unsupported record version %d", ErrCorrupt, envelope.Version)
	}
	nonce, err := base64.RawStdEncoding.DecodeString(envelope.Nonce)
	if err != nil {
		return Record{}, fmt.Errorf("%w: invalid record nonce", ErrCorrupt)
	}
	ciphertext, err := base64.RawStdEncoding.DecodeString(envelope.Ciphertext)
	if err != nil {
		return Record{}, fmt.Errorf("%w: invalid record ciphertext", ErrCorrupt)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return Record{}, fmt.Errorf("%w: invalid master key", ErrCorrupt)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return Record{}, fmt.Errorf("%w: invalid cipher", ErrCorrupt)
	}
	if len(nonce) != aead.NonceSize() {
		return Record{}, fmt.Errorf("%w: invalid record nonce length", ErrCorrupt)
	}
	plaintext, err := aead.Open(nil, nonce, ciphertext, []byte(id))
	if err != nil {
		return Record{}, fmt.Errorf("%w: authentication failed", ErrCorrupt)
	}
	var record Record
	if err := json.Unmarshal(plaintext, &record); err != nil || record.Generation == "" || !json.Valid(record.Payload) {
		return Record{}, fmt.Errorf("%w: invalid record payload", ErrCorrupt)
	}
	return record, nil
}

func defaultStateDir() (string, error) {
	if path := os.Getenv("XDG_STATE_HOME"); filepath.IsAbs(path) {
		return filepath.Join(path, "wirecmd"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "wirecmd"), nil
}

func ensurePrivateDir(path string) error {
	info, err := os.Lstat(path)
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
		return nil, fmt.Errorf("open OAuth credential lock: %w", err)
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = syscall.Close(fd)
		return nil, fmt.Errorf("open OAuth credential lock: invalid file descriptor")
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	if err := validatePrivateFile(path, info); err != nil {
		file.Close()
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
		return nil, false, fmt.Errorf("open OAuth credential record: %w", err)
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = syscall.Close(fd)
		return nil, false, fmt.Errorf("open OAuth credential record: invalid file descriptor")
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
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
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
	temp, err := os.CreateTemp(dir, ".oauth-*")
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
	return syncDirectory(dir)
}

func removePrivateFile(path string, found *bool) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := validatePrivateFile(path, info); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	*found = true
	return syncDirectory(filepath.Dir(path))
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

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func randomText(random io.Reader, size int) (string, error) {
	data := make([]byte, size)
	if _, err := io.ReadFull(random, data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}
