package engine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultCodexOutputSchema_IsStrictAndValidForCurrentCodex(t *testing.T) {
	var schema map[string]any
	if err := json.Unmarshal([]byte(defaultCodexOutputSchema), &schema); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	if got, ok := schema["additionalProperties"].(bool); !ok || got {
		t.Fatalf("additionalProperties: got %#v want false", schema["additionalProperties"])
	}
	req, ok := schema["required"].([]any)
	if !ok {
		t.Fatalf("required should be an array: %#v", schema["required"])
	}
	want := map[string]bool{"final": true, "summary": true}
	if len(req) != len(want) {
		t.Fatalf("required length: got %d want %d (%#v)", len(req), len(want), req)
	}
	for _, v := range req {
		s, _ := v.(string)
		if !want[s] {
			t.Fatalf("unexpected required key %q (all=%#v)", s, req)
		}
	}
}

func TestRunWithConfig_CLIBackend_OpenAISchemaFallback(t *testing.T) {
	repo := initTestRepo(t)
	logsRoot := t.TempDir()

	pinned := writePinnedCatalog(t)
	cxdbSrv := newCXDBTestServer(t)

	cli := filepath.Join(t.TempDir(), "codex")
	if err := os.WriteFile(cli, []byte(`#!/usr/bin/env bash
set -euo pipefail

schema=""
out=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --output-schema)
      schema="$2"
      shift 2
      ;;
    -o|--output)
      out="$2"
      shift 2
      ;;
    *)
      shift
      ;;
  esac
done

if [[ -n "$schema" ]]; then
  echo 'invalid_json_schema: unsupported additionalProperties' >&2
  exit 1
fi

if [[ -n "$out" ]]; then
  echo '{"final":"ok","summary":"ok"}' > "$out"
fi

cat > status.json <<'JSON'
{"status":"success","notes":"fallback path"}
JSON
echo '{"type":"done","text":"ok"}'
`), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KILROY_CODEX_PATH", cli)

	cfg := &RunConfigFile{Version: 1}
	cfg.Repo.Path = repo
	cfg.CXDB.BinaryAddr = cxdbSrv.BinaryAddr()
	cfg.CXDB.HTTPBaseURL = cxdbSrv.URL()
	cfg.LLM.Providers = map[string]struct {
		Backend BackendKind `json:"backend" yaml:"backend"`
	}{
		"openai": {Backend: BackendCLI},
	}
	cfg.ModelDB.LiteLLMCatalogPath = pinned
	cfg.ModelDB.LiteLLMCatalogUpdatePolicy = "pinned"
	cfg.Git.RunBranchPrefix = "attractor/run"

	dot := []byte(`
digraph G {
  graph [goal="test schema fallback"]
  start [shape=Mdiamond]
  exit  [shape=Msquare]
  a [shape=box, llm_provider=openai, llm_model=gpt-5.2, prompt="say hi"]
  start -> a -> exit
}
`)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := RunWithConfig(ctx, dot, cfg, RunOptions{RunID: "schema-fallback", LogsRoot: logsRoot})
	if err != nil {
		t.Fatalf("RunWithConfig: %v", err)
	}

	assertExists(t, filepath.Join(res.LogsRoot, "a", "stdout.schema_failure.log"))
	assertExists(t, filepath.Join(res.LogsRoot, "a", "stderr.schema_failure.log"))
	assertExists(t, filepath.Join(res.LogsRoot, "a", "stdout.log"))
	assertExists(t, filepath.Join(res.LogsRoot, "a", "status.json"))

	var inv map[string]any
	b, err := os.ReadFile(filepath.Join(res.LogsRoot, "a", "cli_invocation.json"))
	if err != nil {
		t.Fatalf("read cli_invocation.json: %v", err)
	}
	if err := json.Unmarshal(b, &inv); err != nil {
		t.Fatalf("unmarshal cli_invocation.json: %v", err)
	}
	retried, _ := inv["schema_fallback_retry"].(bool)
	if !retried {
		t.Fatalf("expected schema_fallback_retry=true in invocation: %#v", inv)
	}
}

func TestRunWithConfig_CLIBackend_OpenAIStateDBFallbackRetry(t *testing.T) {
	repo := initTestRepo(t)
	logsRoot := t.TempDir()

	pinned := writePinnedCatalog(t)
	cxdbSrv := newCXDBTestServer(t)

	cli := filepath.Join(t.TempDir(), "codex")
	sentinel := filepath.Join(t.TempDir(), "first-attempt-sentinel")
	t.Setenv("KILROY_TEST_STATE_DB_SENTINEL", sentinel)
	if err := os.WriteFile(cli, []byte(`#!/usr/bin/env bash
set -euo pipefail

out=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    -o|--output)
      out="$2"
      shift 2
      ;;
    *)
      shift
      ;;
  esac
done

marker="${KILROY_TEST_STATE_DB_SENTINEL:?missing marker}"
if [[ ! -f "$marker" ]]; then
  touch "$marker"
  echo 'state db missing rollout path for thread test-thread' >&2
  exit 1
fi

if [[ -n "$out" ]]; then
  echo '{"final":"ok","summary":"ok"}' > "$out"
fi
cat > status.json <<'JSON'
{"status":"success","notes":"state-db retry success"}
JSON
echo '{"type":"done","text":"ok"}'
`), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KILROY_CODEX_PATH", cli)

	cfg := &RunConfigFile{Version: 1}
	cfg.Repo.Path = repo
	cfg.CXDB.BinaryAddr = cxdbSrv.BinaryAddr()
	cfg.CXDB.HTTPBaseURL = cxdbSrv.URL()
	cfg.LLM.Providers = map[string]struct {
		Backend BackendKind `json:"backend" yaml:"backend"`
	}{
		"openai": {Backend: BackendCLI},
	}
	cfg.ModelDB.LiteLLMCatalogPath = pinned
	cfg.ModelDB.LiteLLMCatalogUpdatePolicy = "pinned"
	cfg.Git.RunBranchPrefix = "attractor/run"

	dot := []byte(`
digraph G {
  graph [goal="test state db fallback"]
  start [shape=Mdiamond]
  exit  [shape=Msquare]
  a [shape=box, llm_provider=openai, llm_model=gpt-5.2, prompt="say hi"]
  start -> a -> exit
}
`)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := RunWithConfig(ctx, dot, cfg, RunOptions{RunID: "state-db-fallback", LogsRoot: logsRoot})
	if err != nil {
		t.Fatalf("RunWithConfig: %v", err)
	}

	assertExists(t, filepath.Join(res.LogsRoot, "a", "status.json"))
	assertExists(t, filepath.Join(res.LogsRoot, "a", "output.json"))

	var inv map[string]any
	b, err := os.ReadFile(filepath.Join(res.LogsRoot, "a", "cli_invocation.json"))
	if err != nil {
		t.Fatalf("read cli_invocation.json: %v", err)
	}
	if err := json.Unmarshal(b, &inv); err != nil {
		t.Fatalf("unmarshal cli_invocation.json: %v", err)
	}
	retried, _ := inv["state_db_fallback_retry"].(bool)
	if !retried {
		t.Fatalf("expected state_db_fallback_retry=true in invocation: %#v", inv)
	}
}
