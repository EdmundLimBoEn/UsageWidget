//go:build !windows

package server

import (
	"os/exec"
	"syscall"
)

// configureCommandCancellation makes sure helper processes spawned by
// CodexBar do not survive a timed-out collection on Unix systems.
func configureCommandCancellation(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
