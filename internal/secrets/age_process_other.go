//go:build !linux && !darwin

package secrets

import "os/exec"

func configureAgeProcess(*exec.Cmd) {}

func killAgeProcess(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
