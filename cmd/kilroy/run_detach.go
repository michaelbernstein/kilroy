package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

var detachedExecCommand = exec.Command

func launchDetached(args []string, logsRoot string) error {
	if strings.TrimSpace(logsRoot) == "" {
		return fmt.Errorf("logs_root is required for detached runs")
	}
	if err := os.MkdirAll(logsRoot, 0o755); err != nil {
		return err
	}

	outPath := filepath.Join(logsRoot, "run.out")
	outFile, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = outFile.Close() }()

	cmd := detachedExecCommand(os.Args[0], args...)
	cmd.Dir = logsRoot
	cmd.Stdin = nil
	cmd.Stdout = outFile
	cmd.Stderr = outFile
	setDetachAttr(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}

	pidPath := filepath.Join(logsRoot, "run.pid")
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(cmd.Process.Pid)), 0o644); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Process.Release()
		return err
	}
	return cmd.Process.Release()
}

func defaultDetachedLogsRoot(runID string) (string, error) {
	stateHome := strings.TrimSpace(os.Getenv("XDG_STATE_HOME"))
	if stateHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		stateHome = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(stateHome, "kilroy", "attractor", "runs", runID), nil
}
