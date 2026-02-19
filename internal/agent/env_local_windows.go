//go:build windows

package agent

import (
	"os/exec"
	"strconv"
)

func setSysProcAttr(cmd *exec.Cmd) {
	// No process-group setup needed on Windows; taskkill /T handles tree kill.
}

func terminateProcessGroup(pid int) {
	if pid <= 0 {
		return
	}
	_ = exec.Command("taskkill", "/T", "/PID", strconv.Itoa(pid)).Run()
}

func killProcessGroup(pid int) {
	if pid <= 0 {
		return
	}
	_ = exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(pid)).Run()
}
