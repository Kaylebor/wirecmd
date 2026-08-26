//go:build !darwin && !linux

package cli

import (
	"fmt"
	"os/exec"
	"runtime"
)

func browserOpenCommand(string) (*exec.Cmd, error) {
	return nil, fmt.Errorf("browser opening is unsupported on %s", runtime.GOOS)
}
