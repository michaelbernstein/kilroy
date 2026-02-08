package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/strongdm/kilroy/internal/attractor/runtime"
)

func TestResume_LoopRestartUsesBaseLogsRoot(t *testing.T) {
	t.Chdir(t.TempDir())

	repo := t.TempDir()
	runCmd(t, repo, "git", "init")
	runCmd(t, repo, "git", "config", "user.name", "tester")
	runCmd(t, repo, "git", "config", "user.email", "tester@example.com")
	_ = os.WriteFile(filepath.Join(repo, "README.md"), []byte("hello\n"), 0o644)
	runCmd(t, repo, "git", "add", "-A")
	runCmd(t, repo, "git", "commit", "-m", "init")

	dot := []byte(`
digraph G {
  graph [goal="resume-restart", max_restarts="1"]
  start [shape=Mdiamond]
  check [shape=diamond]
  work  [shape=parallelogram, timeout="1s", tool_command="/bin/bash -lc 'sleep 2'"]
  exit  [shape=Msquare]
  start -> check
  check -> exit [condition="outcome=success"]
  check -> work [condition="outcome=fail", loop_restart=true]
  work -> check
}
`)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := Run(ctx, dot, RunOptions{RepoPath: repo, RunBranchPrefix: "attractor/run"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	checkStatus := filepath.Join(res.LogsRoot, "check", "status.json")
	_ = os.WriteFile(checkStatus, []byte(`{"status":"fail","failure_reason":"temporary network error: connection reset by peer"}`), 0o644)

	cpPath := filepath.Join(res.LogsRoot, "checkpoint.json")
	cp, err := runtime.LoadCheckpoint(cpPath)
	if err != nil {
		t.Fatalf("LoadCheckpoint: %v", err)
	}
	cp.CurrentNode = "check"
	cp.CompletedNodes = []string{"start", "check"}
	if err := cp.Save(cpPath); err != nil {
		t.Fatalf("Save checkpoint: %v", err)
	}

	_, err = Resume(ctx, res.LogsRoot)
	if err == nil || !strings.Contains(err.Error(), "loop_restart limit exceeded") {
		t.Fatalf("expected loop_restart limit error, got: %v", err)
	}

	restartDir := filepath.Join(res.LogsRoot, "restart-1")
	if _, err := os.Stat(restartDir); err != nil {
		t.Fatalf("expected restart dir under logs root: %v", err)
	}

	if _, err := os.Stat("restart-1"); err == nil {
		t.Fatalf("unexpected relative restart-1 dir in process CWD")
	}
}
