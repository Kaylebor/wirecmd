//go:build darwin

package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRuntimePathsUseDarwinTemporaryDirectoryWhenXDGIsUnset(t *testing.T) {
	previous, configured := os.LookupEnv("XDG_RUNTIME_DIR")
	if err := os.Unsetenv("XDG_RUNTIME_DIR"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if configured {
			_ = os.Setenv("XDG_RUNTIME_DIR", previous)
		} else {
			_ = os.Unsetenv("XDG_RUNTIME_DIR")
		}
	})

	directory, socket, lock, appErr := runtimePaths()
	if appErr != nil {
		t.Fatalf("runtimePaths() error = %#v", appErr)
	}
	wantDirectory := filepath.Join(os.TempDir(), "wirecmd")
	if directory != wantDirectory || socket != filepath.Join(wantDirectory, "daemon.sock") || lock != filepath.Join(wantDirectory, "daemon.lock") {
		t.Fatalf("runtime paths = %q, %q, %q; want below %q", directory, socket, lock, wantDirectory)
	}
}

func TestDarwinFallbackRuntimeDirectoryRejectsSymlinks(t *testing.T) {
	target := t.TempDir()
	if err := os.Chmod(target, 0o700); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(t.TempDir(), "runtime-link")
	if err := os.Symlink(target, symlink); err != nil {
		t.Fatal(err)
	}
	if _, _, _, appErr := runtimePathsFor(symlink, true); appErr == nil || appErr.code != "runtime_dir_unsafe" {
		t.Fatalf("runtimePathsFor() error = %#v, want runtime_dir_unsafe", appErr)
	}
}
