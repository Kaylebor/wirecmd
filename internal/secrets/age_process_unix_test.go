//go:build linux || darwin

package secrets

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestAgeProviderTimeoutKillsProcessGroup(t *testing.T) {
	fake := writeFakeAge(t)
	t.Setenv("AGE_TEST_VERSION", "1.3.2")
	t.Setenv("AGE_TEST_MODE", "spawn")
	childPIDPath := filepath.Join(t.TempDir(), "child.pid")
	t.Setenv("AGE_TEST_CHILD_PID", childPIDPath)
	provider, err := NewAgeProvider(AgeProviderOptions{
		Command:    fake,
		Identities: []string{testIdentity(t)},
		Stores:     []Store{{Path: writeStore(t, `{"TOKEN":"value"}`)}},
		Timeout:    100 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = provider.Resolve(context.Background(), Scope("workspace"), []string{"TOKEN"})
	if got := errorCode(t, err); got != CodeAgeDecryptionTimeout {
		t.Fatalf("code = %q, want %q", got, CodeAgeDecryptionTimeout)
	}

	data, err := os.ReadFile(childPIDPath)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		t.Fatalf("child pid = %q", data)
	}
	deadline := time.Now().Add(time.Second)
	for {
		err = syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("age child process %d remained alive after cancellation: %v", pid, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
