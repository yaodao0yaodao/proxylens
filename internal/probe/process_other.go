//go:build !linux && !windows

package probe

import "os/exec"

func configureCommand(_ *exec.Cmd) {}

func terminateCommand(command *exec.Cmd) {
	if command.Process != nil {
		_ = command.Process.Kill()
	}
}
