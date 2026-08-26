//go:build linux

package cli

import "os/exec"

func browserOpenCommand(raw string) (*exec.Cmd, error) {
	return exec.Command("xdg-open", raw), nil
}
