//go:build !windows

package engine

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func mustReadPIDFile(t *testing.T, path string) int {
	t.Helper()
	return mustReadPIDFileWithin(t, path, 2*time.Second)
}

func mustReadPIDFileWithin(t *testing.T, path string, timeout time.Duration) int {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		b, err := os.ReadFile(path)
		if err == nil {
			pid, convErr := strconv.Atoi(strings.TrimSpace(string(b)))
			if convErr != nil || pid <= 0 {
				t.Fatalf("invalid pid in %s: %q (%v)", path, strings.TrimSpace(string(b)), convErr)
			}
			return pid
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("read pid file %s: %v", path, err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for pid file %s", path)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func waitForPIDToExit(t *testing.T, pid int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if !pidRunning(pid) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("pid %d still running after %s", pid, timeout)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func pidRunning(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
