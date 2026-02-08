package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/strongdm/kilroy/internal/attractor/model"
	"github.com/strongdm/kilroy/internal/attractor/runtime"
)

type scriptedOutcomeHandler struct {
	outcomes []runtime.Outcome
	calls    int
}

func (h *scriptedOutcomeHandler) Execute(ctx context.Context, exec *Execution, node *model.Node) (runtime.Outcome, error) {
	_ = ctx
	_ = exec
	_ = node
	h.calls++
	if len(h.outcomes) == 0 {
		return runtime.Outcome{Status: runtime.StatusFail, FailureReason: "no scripted outcomes"}, nil
	}
	idx := h.calls - 1
	if idx >= len(h.outcomes) {
		idx = len(h.outcomes) - 1
	}
	return h.outcomes[idx], nil
}

func TestRun_DeterministicFailure_DoesNotRetry(t *testing.T) {
	logsRoot := t.TempDir()
	handler := &scriptedOutcomeHandler{
		outcomes: []runtime.Outcome{
			{
				Status:        runtime.StatusFail,
				FailureReason: "provider contract mismatch",
				Meta: map[string]any{
					"failure_class": failureClassDeterministic,
				},
			},
		},
	}
	eng, node := newRetryGateTestEngine(t, logsRoot, 3, handler)

	out, err := eng.executeWithRetry(context.Background(), node, map[string]int{})
	if err != nil {
		t.Fatalf("executeWithRetry: %v", err)
	}
	if out.Status != runtime.StatusFail {
		t.Fatalf("status: got %q want %q", out.Status, runtime.StatusFail)
	}
	if handler.calls != 1 {
		t.Fatalf("attempts: got %d want 1", handler.calls)
	}
	if hasProgressEvent(t, logsRoot, "stage_retry_sleep") {
		t.Fatalf("unexpected stage_retry_sleep for deterministic failure")
	}
	if !hasProgressEvent(t, logsRoot, "stage_retry_blocked") {
		t.Fatalf("expected stage_retry_blocked event for deterministic failure")
	}
}

func TestRun_TransientFailure_StillRetries(t *testing.T) {
	logsRoot := t.TempDir()
	handler := &scriptedOutcomeHandler{
		outcomes: []runtime.Outcome{
			{
				Status:        runtime.StatusFail,
				FailureReason: "upstream timeout",
				Meta: map[string]any{
					"failure_class": failureClassTransientInfra,
				},
			},
			{
				Status: runtime.StatusSuccess,
				Notes:  "ok after retry",
			},
		},
	}
	eng, node := newRetryGateTestEngine(t, logsRoot, 1, handler)

	out, err := eng.executeWithRetry(context.Background(), node, map[string]int{})
	if err != nil {
		t.Fatalf("executeWithRetry: %v", err)
	}
	if out.Status != runtime.StatusSuccess {
		t.Fatalf("status: got %q want %q", out.Status, runtime.StatusSuccess)
	}
	if handler.calls != 2 {
		t.Fatalf("attempts: got %d want 2", handler.calls)
	}
	if !hasProgressEvent(t, logsRoot, "stage_retry_sleep") {
		t.Fatalf("expected stage_retry_sleep for transient failure retry")
	}
}

func newRetryGateTestEngine(t *testing.T, logsRoot string, maxRetries int, handler Handler) (*Engine, *model.Node) {
	t.Helper()

	dot := []byte(fmt.Sprintf(`
digraph G {
  graph [retry.backoff.initial_delay_ms=0]
  start [shape=Mdiamond]
  r [shape=diamond, type="retry_gate_test", max_retries="%d"]
  exit [shape=Msquare]
  start -> r -> exit
}
`, maxRetries))
	g, _, err := Prepare(dot)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	opts := RunOptions{
		RunID:       "retry-gate-test",
		LogsRoot:    logsRoot,
		WorktreeDir: filepath.Join(logsRoot, "worktree"),
	}
	eng := &Engine{
		Graph:           g,
		Options:         opts,
		LogsRoot:        logsRoot,
		WorktreeDir:     opts.WorktreeDir,
		Context:         runtime.NewContext(),
		Registry:        NewDefaultRegistry(),
		Interviewer:     &AutoApproveInterviewer{},
		CodergenBackend: &SimulatedCodergenBackend{},
	}
	eng.Registry.Register("retry_gate_test", handler)
	node := g.Nodes["r"]
	if node == nil {
		t.Fatalf("missing node r")
	}
	return eng, node
}

func hasProgressEvent(t *testing.T, logsRoot, wantEvent string) bool {
	t.Helper()
	lines := mustReadProgressEvents(t, filepath.Join(logsRoot, "progress.ndjson"))
	for _, ev := range lines {
		if got, _ := ev["event"].(string); got == wantEvent {
			return true
		}
	}
	return false
}

func mustReadProgressEvents(t *testing.T, progressPath string) []map[string]any {
	t.Helper()
	b, err := os.ReadFile(progressPath)
	if err != nil {
		t.Fatalf("read %s: %v", progressPath, err)
	}
	rows := splitLines(string(b))
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		var ev map[string]any
		if err := json.Unmarshal([]byte(row), &ev); err != nil {
			t.Fatalf("decode progress row %q: %v", row, err)
		}
		out = append(out, ev)
	}
	return out
}

func splitLines(s string) []string {
	lines := []string{}
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] != '\n' {
			continue
		}
		if i > start {
			lines = append(lines, s[start:i])
		}
		start = i + 1
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}
