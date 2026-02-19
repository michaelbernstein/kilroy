//go:build windows

package engine

import (
	"os/exec"
	"strconv"
)

func setProcessGroupAttr(cmd *exec.Cmd) {
	// No process-group setup needed on Windows; taskkill /T handles tree kill.
}

func hasProcessGroupAttr(cmd *exec.Cmd) bool {
	// Windows does not use Unix process groups, but callers still need the
	// "can we clean up the process tree?" semantic, so return true when the
	// command has a live process.
	return cmd != nil && cmd.Process != nil
}

func terminateProcessGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return exec.Command("taskkill", "/T", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
}

func forceKillProcessGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
}

func forceKillPIDTree(pid int) error {
	return exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(pid)).Run()
}
