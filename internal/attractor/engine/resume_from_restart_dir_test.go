package engine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/strongdm/kilroy/internal/attractor/runtime"
)

func TestResume_WhenLogsRootIsRestartDir_RestartsFromParentBaseLogsRoot(t *testing.T) {
	base := t.TempDir()
	logsRoot := filepath.Join(base, "restart-7")
	cp := runtime.NewCheckpoint()
	cp.Extra = map[string]any{}

	gotBase, gotRestarts := restoreRestartState(logsRoot, cp)
	if gotBase != base {
		t.Fatalf("base logs root: got %q want %q", gotBase, base)
	}
	if gotRestarts != 7 {
		t.Fatalf("restart count: got %d want 7", gotRestarts)
	}
}

func TestResume_RestoreRestartFailureSignatures(t *testing.T) {
	cp := runtime.NewCheckpoint()
	cp.Extra = map[string]any{
		"restart_failure_signatures": map[string]any{
			"check|transient_infra|connection reset": float64(2),
			"check|deterministic|compile error":      float64(1),
			"bad":                                    float64(-1),
		},
	}

	got := restoreRestartFailureSignatures(cp)
	if got["check|transient_infra|connection reset"] != 2 {
		t.Fatalf("transient signature count = %d, want 2", got["check|transient_infra|connection reset"])
	}
	if got["check|deterministic|compile error"] != 1 {
		t.Fatalf("deterministic signature count = %d, want 1", got["check|deterministic|compile error"])
	}
	if _, ok := got["bad"]; ok {
		t.Fatalf("unexpected negative signature count restored: %v", got)
	}
}

func TestResume_RestoresAbsoluteStateRootForCodex(t *testing.T) {
	wd := t.TempDir()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(wd); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() { _ = os.Chdir(oldWD) }()
	stateBase := filepath.Join(wd, ".codex-state-base")
	t.Setenv("KILROY_CODEX_STATE_BASE", stateBase)

	relStageDir := filepath.Join("restart-4", "a")
	env, meta, err := buildCodexIsolatedEnvWithName(relStageDir, "codex-home", os.Environ())
	if err != nil {
		t.Fatalf("buildCodexIsolatedEnvWithName: %v", err)
	}

	stateRoot := strings.TrimSpace(anyToString(meta["state_root"]))
	if !filepath.IsAbs(stateRoot) {
		t.Fatalf("state_root should be absolute, got %q", stateRoot)
	}
	wantPrefix := filepath.Join(stateBase, "codex-home-")
	if !strings.HasPrefix(stateRoot, wantPrefix) {
		t.Fatalf("state_root should be under %q, got %q", wantPrefix, stateRoot)
	}
	if got := envLookup(env, "CODEX_HOME"); got != stateRoot {
		t.Fatalf("CODEX_HOME = %q, want %q", got, stateRoot)
	}
}

func TestResume_FatalBeforeEngineInit_WritesFallbackFinalJSON(t *testing.T) {
	logsRoot := t.TempDir()
	m := manifest{
		RunID:     "resume-fallback-final",
		RepoPath:  t.TempDir(),
		RunBranch: "attractor/run/resume-fallback-final",
	}
	if err := writeJSON(filepath.Join(logsRoot, "manifest.json"), m); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	_, err := Resume(context.Background(), logsRoot)
	if err == nil {
		t.Fatalf("expected resume error, got nil")
	}

	finalPath := filepath.Join(logsRoot, "final.json")
	b, readErr := os.ReadFile(finalPath)
	if readErr != nil {
		t.Fatalf("read fallback final.json: %v", readErr)
	}
	var final runtime.FinalOutcome
	if err := json.Unmarshal(b, &final); err != nil {
		t.Fatalf("unmarshal fallback final.json: %v", err)
	}
	if final.Status != runtime.FinalFail {
		t.Fatalf("final status = %q, want %q", final.Status, runtime.FinalFail)
	}
	if final.RunID != "resume-fallback-final" {
		t.Fatalf("final run_id = %q, want %q", final.RunID, "resume-fallback-final")
	}
	if !strings.Contains(final.FailureReason, "checkpoint.json") {
		t.Fatalf("final failure_reason = %q, want checkpoint.json reference", final.FailureReason)
	}
}
