package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestAttractorRun_DetachedModeSurvivesLauncherExit(t *testing.T) {
	bin := buildKilroyBinary(t)
	cxdb := newCXDBTestServer(t)
	repo := initTestRepo(t)
	catalog := writePinnedCatalog(t)
	cfg := writeRunConfig(t, repo, cxdb.URL(), cxdb.BinaryAddr(), catalog)
	graph := writeDetachGraph(t)
	logs := filepath.Join(t.TempDir(), "logs")

	cmd := exec.Command(
		bin,
		"attractor", "run",
		"--detach",
		"--graph", graph,
		"--config", cfg,
		"--run-id", "detach-smoke",
		"--logs-root", logs,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("detached launch failed: %v\n%s", err, out)
	}

	pidPath := filepath.Join(logs, "run.pid")
	waitForFile(t, pidPath, 5*time.Second)
	pid := readPIDFile(t, pidPath)
	waitForFile(t, filepath.Join(logs, "final.json"), 20*time.Second)
	waitForProcessExit(t, pid, 10*time.Second)
}

func TestAttractorRun_DetachedWritesPIDFile(t *testing.T) {
	bin := buildKilroyBinary(t)
	cxdb := newCXDBTestServer(t)
	repo := initTestRepo(t)
	catalog := writePinnedCatalog(t)
	cfg := writeRunConfig(t, repo, cxdb.URL(), cxdb.BinaryAddr(), catalog)
	graph := writeDetachGraph(t)
	logs := filepath.Join(t.TempDir(), "logs")

	cmd := exec.Command(
		bin,
		"attractor", "run",
		"--detach",
		"--graph", graph,
		"--config", cfg,
		"--run-id", "detach-pid",
		"--logs-root", logs,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("detached launch failed: %v\n%s", err, out)
	}

	pidPath := filepath.Join(logs, "run.pid")
	waitForFile(t, pidPath, 5*time.Second)
	pid := readPIDFile(t, pidPath)

	waitForFile(t, filepath.Join(logs, "final.json"), 20*time.Second)
	waitForProcessExit(t, pid, 10*time.Second)
}

func writeDetachGraph(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "g.dot")
	_ = os.WriteFile(path, []byte(`
digraph G {
  start [shape=Mdiamond]
  t [shape=parallelogram, tool_command="sleep 1"]
  exit [shape=Msquare]
  start -> t -> exit
}`), 0o644)
	return path
}

func waitForFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("file not written within %s: %s", timeout, path)
}

func readPIDFile(t *testing.T, pidPath string) int {
	t.Helper()
	raw, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatalf("read %s: %v", pidPath, err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 0 {
		t.Fatalf("run.pid should contain a positive integer pid, got %q (err=%v)", strings.TrimSpace(string(raw)), err)
	}
	return pid
}

func waitForProcessExit(t *testing.T, pid int, timeout time.Duration) {
	t.Helper()
	if runtime.GOOS != "linux" {
		return
	}
	procPath := filepath.Join("/proc", strconv.Itoa(pid))
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(procPath); os.IsNotExist(err) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("detached process %d still running after %s", pid, timeout)
}
