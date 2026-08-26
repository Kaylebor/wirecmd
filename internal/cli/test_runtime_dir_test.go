package cli

import (
	"os"
	"testing"
)

func testRuntimeDirectory(t *testing.T) string {
	t.Helper()
	directory, err := os.MkdirTemp("", "w")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		_ = os.RemoveAll(directory)
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	return directory
}
