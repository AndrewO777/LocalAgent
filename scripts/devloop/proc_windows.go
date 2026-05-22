//go:build windows

package main

import (
	"os/exec"
	"strconv"
	"syscall"
)

// CREATE_NEW_PROCESS_GROUP gives the child its own console process group so
// taskkill can find the whole tree (npm → node → vite → esbuild → ...).
const createNewProcessGroup = 0x00000200

func setProcessAttrs(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewProcessGroup}
}

// killTree shells out to taskkill /F /T which terminates the target PID
// AND every descendant. Errors are swallowed — they usually mean
// "process already gone", which is fine.
func killTree(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	_ = exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
	return nil
}
