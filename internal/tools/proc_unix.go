//go:build !windows

package tools

import (
	"os/exec"
	"syscall"
)

// Setpgid puts the child in its own process group so we can SIGKILL the whole
// group via the negated PID.
func setProcessAttrs(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killProcessTree(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	return nil
}
