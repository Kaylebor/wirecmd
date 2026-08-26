//go:build darwin

package cli

import "os/exec"

func browserOpenCommand(raw string) (*exec.Cmd, error) {
	return exec.Command("/usr/bin/open", raw), nil
}
