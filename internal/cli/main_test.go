package cli

import (
	"fmt"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	stateHome, err := os.MkdirTemp("", "wirecmd-cli-test-state-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.Setenv("XDG_STATE_HOME", stateHome); err != nil {
		fmt.Fprintln(os.Stderr, err)
		_ = os.RemoveAll(stateHome)
		os.Exit(1)
	}
	code := m.Run()
	if err := os.RemoveAll(stateHome); err != nil && code == 0 {
		fmt.Fprintln(os.Stderr, err)
		code = 1
	}
	os.Exit(code)
}
