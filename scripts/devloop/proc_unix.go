//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// setProcessAttrs puts the child in its own process group so we can signal
// the whole tree (including grandchildren like esbuild / node workers) on
// cancellation, not just the direct child.
func setProcessAttrs(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killTree sends SIGTERM to the process group. The negative PID is the
// magic that targets the group rather than the lone leader.
func killTree(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	return nil
}
