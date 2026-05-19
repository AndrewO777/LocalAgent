//go:build windows

package tools

import (
	"os/exec"
	"strconv"
	"syscall"
)

// CREATE_NEW_PROCESS_GROUP — needed so the child PID is its own group and we
// can identify the tree for taskkill.
const createNewProcessGroup = 0x00000200

func setProcessAttrs(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewProcessGroup}
}

// killProcessTree terminates cmd and every child it spawned. On Windows the
// only reliable way is `taskkill /F /T /PID`. Errors are intentionally
// swallowed: the most likely cause is "process already exited".
func killProcessTree(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	kill := exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(cmd.Process.Pid))
	_ = kill.Run()
	return nil
}
