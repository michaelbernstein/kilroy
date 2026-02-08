package engine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/strongdm/kilroy/internal/attractor/runtime"
)

func TestResume_ParallelBranchNamesUseConfiguredPrefix(t *testing.T) {
	repo := t.TempDir()
	runCmd(t, repo, "git", "init")
	runCmd(t, repo, "git", "config", "user.name", "tester")
	runCmd(t, repo, "git", "config", "user.email", "tester@example.com")
	_ = os.WriteFile(filepath.Join(repo, "README.md"), []byte("hello\n"), 0o644)
	runCmd(t, repo, "git", "add", "-A")
	runCmd(t, repo, "git", "commit", "-m", "init")

	dot := []byte(`
digraph P {
  graph [goal="test"]
  start [shape=Mdiamond]
  par [shape=component]
  a [shape=box, llm_provider=openai, llm_model=gpt-5.2, prompt="a"]
  b [shape=box, llm_provider=openai, llm_model=gpt-5.2, prompt="b"]
  join [shape=tripleoctagon]
  exit [shape=Msquare]
  start -> par
  par -> a
  par -> b
  a -> join
  b -> join
  join -> exit
}
`)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	res, err := Run(ctx, dot, RunOptions{RepoPath: repo, RunBranchPrefix: "attractor/run"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	cpPath := filepath.Join(res.LogsRoot, "checkpoint.json")
	cp, err := runtime.LoadCheckpoint(cpPath)
	if err != nil {
		t.Fatalf("LoadCheckpoint: %v", err)
	}
	cp.CurrentNode = "start"
	cp.CompletedNodes = []string{"start"}
	if err := cp.Save(cpPath); err != nil {
		t.Fatalf("Save checkpoint: %v", err)
	}

	if _, err := Resume(ctx, res.LogsRoot); err != nil {
		t.Fatalf("Resume: %v", err)
	}

	b, err := os.ReadFile(filepath.Join(res.LogsRoot, "par", "parallel_results.json"))
	if err != nil {
		t.Fatalf("read parallel_results.json: %v", err)
	}
	var rows []map[string]any
	if err := json.Unmarshal(b, &rows); err != nil {
		t.Fatalf("unmarshal parallel_results.json: %v", err)
	}
	for _, row := range rows {
		got := strings.TrimSpace(anyToString(row["branch_name"]))
		if strings.HasPrefix(got, "/parallel/") {
			t.Fatalf("invalid branch namespace after resume: %q", got)
		}
		if !strings.HasPrefix(got, "attractor/run/parallel/") {
			t.Fatalf("expected attractor/run/parallel prefix, got %q", got)
		}
	}
}
