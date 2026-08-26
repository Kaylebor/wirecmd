package oauthstore

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	keyring "github.com/zalando/go-keyring"
)

type memoryKeyring struct {
	mu     sync.Mutex
	values map[string]string
	err    error
}

func (m *memoryKeyring) Get(service, account string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return "", m.err
	}
	value, ok := m.values[service+"\x00"+account]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return value, nil
}

func (m *memoryKeyring) Set(service, account, secret string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	m.values[service+"\x00"+account] = secret
	return nil
}

func (m *memoryKeyring) Delete(service, account string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	delete(m.values, service+"\x00"+account)
	return nil
}

func newTestStore(t *testing.T) (*Store, *memoryKeyring, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "wirecmd")
	keys := &memoryKeyring{values: make(map[string]string)}
	store, err := Open(Options{StateDir: dir, Keyring: keys, Random: bytes.NewReader(bytes.Repeat([]byte{0x42}, 4096))})
	if err != nil {
		t.Fatal(err)
	}
	return store, keys, dir
}

func TestSaveLoadDeleteEncryptedRecord(t *testing.T) {
	store, _, stateDir := newTestStore(t)
	identity := []byte("resource=https://example.test/mcp\x00issuer=https://issuer.test")
	input := Record{Payload: []byte(`{"token":"[REDACTED]","refresh":"[REDACTED]"}`)}
	saved, err := store.Save(identity, input)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Generation == "" {
		t.Fatal("Save did not generate a record generation")
	}
	loaded, found, err := store.Load(identity)
	if err != nil {
		t.Fatal(err)
	}
	if !found || loaded.Generation != saved.Generation || string(loaded.Payload) != string(input.Payload) {
		t.Fatalf("Load() = (%#v, %t), want saved record", loaded, found)
	}

	entries, err := os.ReadDir(filepath.Join(stateDir, credentialsDir))
	if err != nil {
		t.Fatal(err)
	}
	var recordPath string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".enc") {
			recordPath = filepath.Join(stateDir, credentialsDir, entry.Name())
		}
	}
	if recordPath == "" || strings.Contains(filepath.Base(recordPath), "example") || strings.Contains(filepath.Base(recordPath), "issuer") {
		t.Fatalf("record path = %q", recordPath)
	}
	raw, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "[REDACTED]") || strings.Contains(string(raw), "token") {
		t.Fatalf("encrypted record contains plaintext: %q", raw)
	}
	assertMode(t, stateDir, 0o700)
	assertMode(t, filepath.Join(stateDir, credentialsDir), 0o700)
	assertMode(t, recordPath, 0o600)
	assertMode(t, filepath.Join(stateDir, credentialsDir, credentialsLock), 0o600)

	found, err = store.Delete(identity)
	if err != nil || !found {
		t.Fatalf("Delete() = (%t, %v), want true, nil", found, err)
	}
	_, found, err = store.Load(identity)
	if err != nil || found {
		t.Fatalf("Load after Delete = (%t, %v), want false, nil", found, err)
	}
}

func TestStorePreservesGenerationAndSeparatesOpaqueIdentities(t *testing.T) {
	store, _, stateDir := newTestStore(t)
	first, err := store.Save([]byte("first secret identity"), Record{Payload: []byte(`{"one":1}`)})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Save([]byte("first secret identity"), Record{Generation: first.Generation, Payload: []byte(`{"one":2}`)})
	if err != nil {
		t.Fatal(err)
	}
	if second.Generation != first.Generation {
		t.Fatalf("generation changed: %q != %q", second.Generation, first.Generation)
	}
	if _, err := store.Save([]byte("second secret identity"), Record{Payload: []byte(`{"two":2}`)}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(stateDir, credentialsDir))
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".enc") {
			count++
			if strings.Contains(entry.Name(), "secret") {
				t.Fatalf("identity leaked into filename %q", entry.Name())
			}
		}
	}
	if count != 2 {
		t.Fatalf("record count = %d, want 2", count)
	}
}

func TestLoadDistinguishesAbsentUnavailableAndCorrupt(t *testing.T) {
	store, keys, stateDir := newTestStore(t)
	identity := []byte("identity")
	if _, found, err := store.Load(identity); err != nil || found {
		t.Fatalf("missing Load() = (%t, %v), want false, nil", found, err)
	}
	keys.err = errors.New("keyring locked")
	if _, _, err := store.Load(identity); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("unavailable Load() error = %v", err)
	}
	keys.err = nil
	if _, err := store.Save(identity, Record{Payload: []byte(`{"ok":true}`)}); err != nil {
		t.Fatal(err)
	}
	recordPath := onlyRecordPath(t, filepath.Join(stateDir, credentialsDir))
	if err := os.WriteFile(recordPath, []byte(`{"version":999}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Load(identity); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("corrupt Load() error = %v", err)
	}
}

func TestStoreRejectsUnsafeState(t *testing.T) {
	store, _, stateDir := newTestStore(t)
	identity := []byte("identity")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save(identity, Record{Payload: []byte(`{}`)}); !errors.Is(err, ErrUnsafeState) {
		t.Fatalf("unsafe directory error = %v", err)
	}

	safeStore, _, safeState := newTestStore(t)
	if _, err := safeStore.Save(identity, Record{Payload: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	recordPath := onlyRecordPath(t, filepath.Join(safeState, credentialsDir))
	if err := os.Remove(recordPath); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(safeState, "target")
	if err := os.WriteFile(target, []byte("not a record"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, recordPath); err != nil {
		t.Fatal(err)
	}
	if _, _, err := safeStore.Load(identity); !errors.Is(err, ErrUnsafeState) {
		t.Fatalf("symlink record error = %v", err)
	}
}

func TestStoreRejectsInvalidPayloadAndIdentity(t *testing.T) {
	store, _, _ := newTestStore(t)
	if _, err := store.Save(nil, Record{Payload: []byte(`{}`)}); err == nil {
		t.Fatal("Save(nil) succeeded")
	}
	if _, err := store.Save([]byte("identity"), Record{Payload: []byte(`not json`)}); err == nil {
		t.Fatal("Save(invalid JSON) succeeded")
	}
}

func TestSaveRejectsStaleGenerationAfterReplaceOrDelete(t *testing.T) {
	store, _, _ := newTestStore(t)
	identity := []byte("identity")
	first, err := store.Save(identity, Record{Payload: []byte(`{"token":"first"}`)})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Save(identity, Record{Payload: []byte(`{"token":"second"}`)})
	if err != nil {
		t.Fatal(err)
	}
	first.Generation = "known-stale-generation"
	first.Payload = []byte(`{"token":"stale"}`)
	if _, err := store.Save(identity, first); !errors.Is(err, ErrStale) {
		t.Fatalf("stale replacement error = %v", err)
	}
	if _, err := store.Delete(identity); err != nil {
		t.Fatal(err)
	}
	second.Payload = []byte(`{"token":"resurrected"}`)
	if _, err := store.Save(identity, second); !errors.Is(err, ErrStale) {
		t.Fatalf("stale resurrection error = %v", err)
	}
	if _, found, err := store.Load(identity); err != nil || found {
		t.Fatalf("deleted record returned found=%v err=%v", found, err)
	}
}

func TestAuthorizationLockIsNonblockingAndIdentityScoped(t *testing.T) {
	store, _, _ := newTestStore(t)
	release, err := store.LockAuthorization([]byte("same identity"))
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := store.LockAuthorization([]byte("same identity")); !errors.Is(err, ErrAuthorizationLocked) {
		t.Fatalf("second identity lock error = %v", err)
	}
	otherRelease, err := store.LockAuthorization([]byte("other identity"))
	if err != nil {
		t.Fatalf("other identity lock: %v", err)
	}
	otherRelease()
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("mode for %q = %o, want %o", path, got, want)
	}
}

func onlyRecordPath(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".enc") {
			return filepath.Join(dir, entry.Name())
		}
	}
	t.Fatal("no record file")
	return ""
}
