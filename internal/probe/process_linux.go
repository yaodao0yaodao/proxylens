//go:build linux

package probe

import (
	"os/exec"
	"syscall"
)

func configureCommand(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
}

func terminateCommand(command *exec.Cmd) {
	if command.Process == nil {
		return
	}
	// The negative PID addresses the dedicated process group. This also covers
	// helper processes a future sing-box build might create.
	_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
}
