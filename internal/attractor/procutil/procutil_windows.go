//go:build windows

package procutil

import "syscall"

const processQueryLimitedInformation = 0x1000

// PIDAlive reports whether a process exists and is not a zombie.
func PIDAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		return false
	}
	syscall.CloseHandle(h)
	return true
}
