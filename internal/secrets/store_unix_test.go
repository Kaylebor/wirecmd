//go:build linux || darwin

package secrets

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestReadAgeStoreRejectsFIFOWithoutBlocking(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.json.age")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	_, _, err := readAgeStore(path, 1024)
	if got := errorCode(t, err); got != CodeSecretStoreInvalid {
		t.Fatalf("code = %q, want %q", got, CodeSecretStoreInvalid)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("FIFO rejection took %s", elapsed)
	}
}

func TestReadAgeStoreRejectsParentDirectoryReplacement(t *testing.T) {
	workspace := t.TempDir()
	directory := filepath.Join(workspace, ".wirecmd")
	path := filepath.Join(directory, "secrets.json.age")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secrets.json.age"), []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(directory); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, directory); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	_, _, err := readAgeStore(path, 1024)
	if got := errorCode(t, err); got != CodeSecretStoreInvalid {
		t.Fatalf("code = %q, want %q", got, CodeSecretStoreInvalid)
	}
}
