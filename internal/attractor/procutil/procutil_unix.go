//go:build !windows

package procutil

import (
	"errors"
	"syscall"
)

// PIDAlive reports whether a process exists and is not a zombie.
func PIDAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	if PIDZombie(pid) {
		return false
	}
	err := syscall.Kill(pid, 0)
	if err == nil {
		return true
	}
	return errors.Is(err, syscall.EPERM)
}
