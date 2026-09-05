//go:build !windows

package dependency

import "os/exec"

func configureCommand(_ *exec.Cmd) {}
