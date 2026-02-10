package engine

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/strongdm/kilroy/internal/attractor/runtime"
)

// Intentionally uses shape=parallelogram/tool_command because this is the
// existing supported ToolHandler path in the current engine.
func TestRun_GlobalStageTimeoutCapsToolNode(t *testing.T) {
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("requires sleep binary")
	}
	dot := []byte(`digraph G {
  start [shape=Mdiamond]
  wait [shape=parallelogram, tool_command="sleep 2"]
  exit [shape=Msquare]
  start -> wait
  wait -> exit [condition="outcome=success"]
}`)
	repo := initTestRepo(t)
	opts := RunOptions{RepoPath: repo, StageTimeout: 100 * time.Millisecond}
	_, err := Run(context.Background(), dot, opts)
	if err == nil {
		t.Fatal("expected stage timeout error")
	}
}

func TestRun_GlobalAndNodeTimeout_UsesSmallerTimeout(t *testing.T) {
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("requires sleep binary")
	}
	dot := []byte(`digraph G {
  start [shape=Mdiamond]
  wait [shape=parallelogram, timeout="1s", tool_command="sleep 2"]
  exit [shape=Msquare]
  start -> wait
  wait -> exit [condition="outcome=success"]
}`)
	repo := initTestRepo(t)
	opts := RunOptions{RepoPath: repo, StageTimeout: 5 * time.Second}
	_, err := Run(context.Background(), dot, opts)
	if err == nil {
		t.Fatal("expected timeout from node/global min timeout")
	}
}

// TestRun_TimeoutOutcomeIncludesMetadata verifies that timed-out nodes get
// enriched Meta with timeout=true and a partial_status.json diagnostic artifact.
// Uses StageTimeout (engine-level) rather than node timeout to ensure the engine
// context deadline fires — the ToolHandler applies node timeout internally.
func TestRun_TimeoutOutcomeIncludesMetadata(t *testing.T) {
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("requires sleep binary")
	}
	dot := []byte(`digraph G {
  start [shape=Mdiamond]
  wait [shape=parallelogram, tool_command="sleep 5"]
  exit [shape=Msquare]
  start -> wait
  wait -> exit [condition="outcome=success"]
}`)
	repo := initTestRepo(t)
	logsRoot := t.TempDir()
	// Use global StageTimeout to set the engine-level context deadline.
	opts := RunOptions{RepoPath: repo, LogsRoot: logsRoot, StageTimeout: 500 * time.Millisecond}
	_, _ = Run(context.Background(), dot, opts)

	statusPath := filepath.Join(logsRoot, "wait", "status.json")
	b, err := os.ReadFile(statusPath)
	if err != nil {
		t.Fatalf("read status.json: %v", err)
	}
	out, err := runtime.DecodeOutcomeJSON(b)
	if err != nil {
		t.Fatalf("decode status.json: %v", err)
	}
	if out.Meta == nil {
		t.Fatal("expected Meta to be populated on timeout outcome")
	}
	if v, ok := out.Meta["timeout"]; !ok || v != true {
		t.Fatalf("Meta[timeout]: got %v want true", out.Meta["timeout"])
	}

	// Also verify partial_status.json was written.
	partialPath := filepath.Join(logsRoot, "wait", "partial_status.json")
	pb, err := os.ReadFile(partialPath)
	if err != nil {
		t.Fatalf("read partial_status.json: %v", err)
	}
	var partial map[string]any
	if err := json.Unmarshal(pb, &partial); err != nil {
		t.Fatalf("unmarshal partial_status.json: %v", err)
	}
	if partial["harvested"] != true {
		t.Fatalf("partial_status.json: expected harvested=true, got %v", partial["harvested"])
	}
}
