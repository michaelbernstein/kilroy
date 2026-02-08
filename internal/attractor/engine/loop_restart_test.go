package engine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/strongdm/kilroy/internal/attractor/model"
	"github.com/strongdm/kilroy/internal/attractor/runtime"
)

func TestRun_LoopRestartCreatesNewLogDirectory(t *testing.T) {
	repo := t.TempDir()
	runCmd(t, repo, "git", "init")
	runCmd(t, repo, "git", "config", "user.name", "tester")
	runCmd(t, repo, "git", "config", "user.email", "tester@example.com")
	_ = os.WriteFile(filepath.Join(repo, "README.md"), []byte("hello\n"), 0o644)
	runCmd(t, repo, "git", "add", "-A")
	runCmd(t, repo, "git", "commit", "-m", "init")

	// Graph: start -> work -> check
	//   check -> exit [condition="outcome=success"]
	//   check -> work [condition="outcome=fail", loop_restart=true]
	//
	// The backend returns fail on the first call to "work", success on the second.
	dot := []byte(`
digraph G {
  graph [goal="test loop restart"]
  start [shape=Mdiamond]
  exit  [shape=Msquare]
  work  [shape=box, llm_provider=openai, llm_model=gpt-5.2, prompt="do work"]
  check [shape=diamond]
  start -> work
  work -> check
  check -> exit [condition="outcome=success"]
  check -> work [condition="outcome=fail", loop_restart=true]
}
`)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var callCount atomic.Int32
	backend := &countingBackend{
		fn: func(ctx context.Context, exec *Execution, node *model.Node, prompt string) (string, *runtime.Outcome, error) {
			n := callCount.Add(1)
			if node.ID == "work" && n == 1 {
				return "fail", &runtime.Outcome{Status: runtime.StatusFail, FailureReason: "temporary network error: connection reset by peer"}, nil
			}
			return "ok", &runtime.Outcome{Status: runtime.StatusSuccess}, nil
		},
	}

	g, _, err := Prepare(dot)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	logsRoot := t.TempDir()
	eng := &Engine{
		Graph:           g,
		Options:         RunOptions{RepoPath: repo, RunID: "test-restart", LogsRoot: logsRoot, WorktreeDir: filepath.Join(logsRoot, "worktree"), RunBranchPrefix: "attractor/run", RequireClean: true},
		DotSource:       dot,
		LogsRoot:        logsRoot,
		WorktreeDir:     filepath.Join(logsRoot, "worktree"),
		Context:         runtime.NewContext(),
		Registry:        NewDefaultRegistry(),
		Interviewer:     &AutoApproveInterviewer{},
		CodergenBackend: backend,
	}
	eng.RunBranch = "attractor/run/test-restart"

	res, err := eng.run(ctx)
	if err != nil {
		t.Fatalf("run() error: %v", err)
	}
	if res.FinalStatus != runtime.FinalSuccess {
		t.Fatalf("FinalStatus = %v, want success", res.FinalStatus)
	}

	// Verify the backend was called twice for "work" (once per iteration).
	if got := callCount.Load(); got < 2 {
		t.Fatalf("backend call count = %d, want >= 2", got)
	}

	// Verify a restart directory was created.
	restartDir := filepath.Join(logsRoot, "restart-1")
	if _, err := os.Stat(restartDir); err != nil {
		t.Fatalf("expected restart-1 directory to exist: %v", err)
	}

	// Verify manifest.json exists in the restart directory (review fix: metadata in restart dirs).
	manifestPath := filepath.Join(restartDir, "manifest.json")
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("expected manifest.json in restart dir: %v", err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatalf("invalid manifest.json: %v", err)
	}
	if manifest["run_id"] != "test-restart" {
		t.Errorf("manifest run_id = %v, want %q", manifest["run_id"], "test-restart")
	}

	// Verify context was reset on restart (review fix: no stale context bleed).
	// After a successful restart, context should have graph-level attrs but NOT
	// node outcomes from the first (failed) iteration.
	if _, found := eng.Context.Get("node.work.outcome"); found {
		t.Error("stale node outcome leaked across restart boundary")
	}
}

func TestRun_LoopRestartLimitExceeded(t *testing.T) {
	repo := t.TempDir()
	runCmd(t, repo, "git", "init")
	runCmd(t, repo, "git", "config", "user.name", "tester")
	runCmd(t, repo, "git", "config", "user.email", "tester@example.com")
	_ = os.WriteFile(filepath.Join(repo, "README.md"), []byte("hello\n"), 0o644)
	runCmd(t, repo, "git", "add", "-A")
	runCmd(t, repo, "git", "commit", "-m", "init")

	// Always fail, with max_restarts=2 so we hit the limit quickly.
	dot := []byte(`
digraph G {
  graph [goal="test limit", max_restarts="2", loop_restart_signature_limit="99"]
  start [shape=Mdiamond]
  exit  [shape=Msquare]
  work  [shape=box, llm_provider=openai, llm_model=gpt-5.2, prompt="do work"]
  check [shape=diamond]
  start -> work
  work -> check
  check -> exit [condition="outcome=success"]
  check -> work [condition="outcome=fail", loop_restart=true]
}
`)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	backend := &countingBackend{
		fn: func(ctx context.Context, exec *Execution, node *model.Node, prompt string) (string, *runtime.Outcome, error) {
			return "fail", &runtime.Outcome{Status: runtime.StatusFail, FailureReason: "temporary upstream failure: 503 service unavailable"}, nil
		},
	}

	g, _, err := Prepare(dot)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	logsRoot := t.TempDir()
	eng := &Engine{
		Graph:           g,
		Options:         RunOptions{RepoPath: repo, RunID: "test-limit", LogsRoot: logsRoot, WorktreeDir: filepath.Join(logsRoot, "worktree"), RunBranchPrefix: "attractor/run", RequireClean: true},
		DotSource:       dot,
		LogsRoot:        logsRoot,
		WorktreeDir:     filepath.Join(logsRoot, "worktree"),
		Context:         runtime.NewContext(),
		Registry:        NewDefaultRegistry(),
		Interviewer:     &AutoApproveInterviewer{},
		CodergenBackend: backend,
	}
	eng.RunBranch = "attractor/run/test-limit"

	_, err = eng.run(ctx)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "loop_restart limit exceeded") {
		t.Fatalf("expected loop_restart limit error, got: %v", err)
	}
}

func TestRun_LoopRestartLimitExceeded_WritesTerminalFinalJSON(t *testing.T) {
	repo := t.TempDir()
	runCmd(t, repo, "git", "init")
	runCmd(t, repo, "git", "config", "user.name", "tester")
	runCmd(t, repo, "git", "config", "user.email", "tester@example.com")
	_ = os.WriteFile(filepath.Join(repo, "README.md"), []byte("hello\n"), 0o644)
	runCmd(t, repo, "git", "add", "-A")
	runCmd(t, repo, "git", "commit", "-m", "init")

	dot := []byte(`
digraph G {
  graph [goal="test terminal final", max_restarts="1"]
  start [shape=Mdiamond]
  exit  [shape=Msquare]
  work  [shape=box, llm_provider=openai, llm_model=gpt-5.2, prompt="do work"]
  check [shape=diamond]
  start -> work
  work -> check
  check -> exit [condition="outcome=success"]
  check -> work [condition="outcome=fail", loop_restart=true]
}
`)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	backend := &countingBackend{
		fn: func(ctx context.Context, exec *Execution, node *model.Node, prompt string) (string, *runtime.Outcome, error) {
			return "fail", &runtime.Outcome{Status: runtime.StatusFail, FailureReason: "temporary network error: connection reset by peer"}, nil
		},
	}

	g, _, err := Prepare(dot)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	logsRoot := t.TempDir()
	eng := &Engine{
		Graph:           g,
		Options:         RunOptions{RepoPath: repo, RunID: "test-final-on-limit", LogsRoot: logsRoot, WorktreeDir: filepath.Join(logsRoot, "worktree"), RunBranchPrefix: "attractor/run", RequireClean: true},
		DotSource:       dot,
		LogsRoot:        logsRoot,
		WorktreeDir:     filepath.Join(logsRoot, "worktree"),
		Context:         runtime.NewContext(),
		Registry:        NewDefaultRegistry(),
		Interviewer:     &AutoApproveInterviewer{},
		CodergenBackend: backend,
	}
	eng.RunBranch = "attractor/run/test-final-on-limit"

	_, err = eng.run(ctx)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "loop_restart limit exceeded") {
		t.Fatalf("expected loop_restart limit error, got: %v", err)
	}

	baseFinalPath := filepath.Join(logsRoot, "final.json")
	baseBytes, err := os.ReadFile(baseFinalPath)
	if err != nil {
		t.Fatalf("read base final.json: %v", err)
	}
	var baseFinal runtime.FinalOutcome
	if err := json.Unmarshal(baseBytes, &baseFinal); err != nil {
		t.Fatalf("unmarshal base final.json: %v", err)
	}
	if baseFinal.Status != runtime.FinalFail {
		t.Fatalf("base final status = %q, want %q", baseFinal.Status, runtime.FinalFail)
	}
	if !strings.Contains(baseFinal.FailureReason, "loop_restart limit exceeded") {
		t.Fatalf("base final failure_reason = %q, want loop_restart limit", baseFinal.FailureReason)
	}

	restartFinalPath := filepath.Join(logsRoot, "restart-1", "final.json")
	restartBytes, err := os.ReadFile(restartFinalPath)
	if err != nil {
		t.Fatalf("read restart final.json: %v", err)
	}
	var restartFinal runtime.FinalOutcome
	if err := json.Unmarshal(restartBytes, &restartFinal); err != nil {
		t.Fatalf("unmarshal restart final.json: %v", err)
	}
	if restartFinal.Status != runtime.FinalFail {
		t.Fatalf("restart final status = %q, want %q", restartFinal.Status, runtime.FinalFail)
	}
}

func TestRun_LoopRestartBlockedForDeterministicFailureClass(t *testing.T) {
	repo := t.TempDir()
	runCmd(t, repo, "git", "init")
	runCmd(t, repo, "git", "config", "user.name", "tester")
	runCmd(t, repo, "git", "config", "user.email", "tester@example.com")
	_ = os.WriteFile(filepath.Join(repo, "README.md"), []byte("hello\n"), 0o644)
	runCmd(t, repo, "git", "add", "-A")
	runCmd(t, repo, "git", "commit", "-m", "init")

	dot := []byte(`
digraph G {
  graph [goal="test deterministic block", max_restarts="10"]
  start [shape=Mdiamond]
  exit  [shape=Msquare]
  work  [shape=box, llm_provider=openai, llm_model=gpt-5.2, prompt="do work"]
  check [shape=diamond]
  start -> work
  work -> check
  check -> exit [condition="outcome=success"]
  check -> work [condition="outcome=fail", loop_restart=true]
}
`)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	backend := &countingBackend{
		fn: func(ctx context.Context, exec *Execution, node *model.Node, prompt string) (string, *runtime.Outcome, error) {
			return "fail", &runtime.Outcome{Status: runtime.StatusFail, FailureReason: "compile error: missing symbol TraceGlyph"}, nil
		},
	}

	g, _, err := Prepare(dot)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	logsRoot := t.TempDir()
	eng := &Engine{
		Graph:           g,
		Options:         RunOptions{RepoPath: repo, RunID: "test-deterministic-block", LogsRoot: logsRoot, WorktreeDir: filepath.Join(logsRoot, "worktree"), RunBranchPrefix: "attractor/run", RequireClean: true},
		DotSource:       dot,
		LogsRoot:        logsRoot,
		WorktreeDir:     filepath.Join(logsRoot, "worktree"),
		Context:         runtime.NewContext(),
		Registry:        NewDefaultRegistry(),
		Interviewer:     &AutoApproveInterviewer{},
		CodergenBackend: backend,
	}
	eng.RunBranch = "attractor/run/test-deterministic-block"

	_, err = eng.run(ctx)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "loop_restart blocked") {
		t.Fatalf("expected loop_restart blocked error, got: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(logsRoot, "restart-1")); !os.IsNotExist(statErr) {
		t.Fatalf("unexpected restart-1 directory created (err=%v)", statErr)
	}

	finalBytes, err := os.ReadFile(filepath.Join(logsRoot, "final.json"))
	if err != nil {
		t.Fatalf("read final.json: %v", err)
	}
	var final runtime.FinalOutcome
	if err := json.Unmarshal(finalBytes, &final); err != nil {
		t.Fatalf("unmarshal final.json: %v", err)
	}
	if final.Status != runtime.FinalFail {
		t.Fatalf("final status = %q, want %q", final.Status, runtime.FinalFail)
	}
	if !strings.Contains(final.FailureReason, "failure_class=deterministic") {
		t.Fatalf("final failure_reason = %q, want deterministic failure class", final.FailureReason)
	}
}

func TestRun_LoopRestartCircuitBreakerOnRepeatedSignature(t *testing.T) {
	repo := t.TempDir()
	runCmd(t, repo, "git", "init")
	runCmd(t, repo, "git", "config", "user.name", "tester")
	runCmd(t, repo, "git", "config", "user.email", "tester@example.com")
	_ = os.WriteFile(filepath.Join(repo, "README.md"), []byte("hello\n"), 0o644)
	runCmd(t, repo, "git", "add", "-A")
	runCmd(t, repo, "git", "commit", "-m", "init")

	dot := []byte(`
digraph G {
  graph [goal="test circuit breaker", max_restarts="20", loop_restart_signature_limit="2"]
  start [shape=Mdiamond]
  exit  [shape=Msquare]
  work  [shape=box, llm_provider=openai, llm_model=gpt-5.2, prompt="do work"]
  check [shape=diamond]
  start -> work
  work -> check
  check -> exit [condition="outcome=success"]
  check -> work [condition="outcome=fail", loop_restart=true]
}
`)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	backend := &countingBackend{
		fn: func(ctx context.Context, exec *Execution, node *model.Node, prompt string) (string, *runtime.Outcome, error) {
			return "fail", &runtime.Outcome{Status: runtime.StatusFail, FailureReason: "temporary network error: connection reset by peer"}, nil
		},
	}

	g, _, err := Prepare(dot)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	logsRoot := t.TempDir()
	eng := &Engine{
		Graph:           g,
		Options:         RunOptions{RepoPath: repo, RunID: "test-circuit-breaker", LogsRoot: logsRoot, WorktreeDir: filepath.Join(logsRoot, "worktree"), RunBranchPrefix: "attractor/run", RequireClean: true},
		DotSource:       dot,
		LogsRoot:        logsRoot,
		WorktreeDir:     filepath.Join(logsRoot, "worktree"),
		Context:         runtime.NewContext(),
		Registry:        NewDefaultRegistry(),
		Interviewer:     &AutoApproveInterviewer{},
		CodergenBackend: backend,
	}
	eng.RunBranch = "attractor/run/test-circuit-breaker"

	_, err = eng.run(ctx)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "loop_restart circuit breaker") {
		t.Fatalf("expected circuit breaker error, got: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(logsRoot, "restart-1")); statErr != nil {
		t.Fatalf("expected restart-1 directory to exist: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(logsRoot, "restart-2")); !os.IsNotExist(statErr) {
		t.Fatalf("expected restart-2 to not exist after circuit breaker (err=%v)", statErr)
	}

	finalBytes, err := os.ReadFile(filepath.Join(logsRoot, "final.json"))
	if err != nil {
		t.Fatalf("read final.json: %v", err)
	}
	var final runtime.FinalOutcome
	if err := json.Unmarshal(finalBytes, &final); err != nil {
		t.Fatalf("unmarshal final.json: %v", err)
	}
	if final.Status != runtime.FinalFail {
		t.Fatalf("final status = %q, want %q", final.Status, runtime.FinalFail)
	}
	if !strings.Contains(final.FailureReason, "loop_restart circuit breaker") {
		t.Fatalf("final failure_reason = %q, want circuit breaker", final.FailureReason)
	}
}

// countingBackend is a test backend with a configurable function.
type countingBackend struct {
	fn func(ctx context.Context, exec *Execution, node *model.Node, prompt string) (string, *runtime.Outcome, error)
}

func (b *countingBackend) Run(ctx context.Context, exec *Execution, node *model.Node, prompt string) (string, *runtime.Outcome, error) {
	return b.fn(ctx, exec, node, prompt)
}
