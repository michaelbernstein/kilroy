# Fix Environment Asymmetry Across Handler Types

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Eliminate the environment mismatch between tool nodes and codergen nodes that causes toolchain gates to pass while downstream work nodes fail.

**Architecture:** Extract a shared `buildNodeEnv` function that both `ToolHandler` and `CodergenRouter` use, ensuring all handler types see the same toolchain paths (RUSTUP_HOME, CARGO_HOME, CARGO_TARGET_DIR, etc.). Update the english-to-dotfile skill to stop generating toolchain gates that validate the wrong execution environment.

**Tech Stack:** Go (engine), Markdown (skill)

---

## The Problem

The Kilroy Attractor engine has three distinct execution environments depending on node type:

| Handler | How `cmd.Env` is set | HOST toolchain visible? | CARGO_TARGET_DIR? |
|---------|---------------------|------------------------|-------------------|
| **Tool** (`parallelogram`) | `nil` — inherits `os.Environ()` | Yes | No |
| **Codergen + codex** (`box`, openai) | `buildCodexIsolatedEnv()` + overrides HOME | **No** (HOME changed) | Yes |
| **Codergen + other CLI** (`box`, anthropic/google) | `scrubConflictingProviderEnvKeys()` | Yes | No |

This asymmetry causes a specific, repeatable failure pattern:

1. `check_toolchain` (`shape=parallelogram`) runs `command -v cargo` in the **host environment** where `$HOME/.cargo/bin` is on PATH. **Passes.**
2. `implement` (`shape=box`, codex backend) gets `buildCodexIsolatedEnv()` which overrides `HOME` to an isolated temp dir. If `RUSTUP_HOME`/`CARGO_HOME` aren't explicitly set in the system env, cargo/rustup default to `$HOME/.cargo`/`$HOME/.rustup` — which now points to the **empty isolated dir**. Toolchain not found.
3. `verify_impl` (`shape=box`, codex backend) — same isolated env, same failure. **`rust_toolchain_unavailable`.**
4. The pipeline retries identically and loops until it exhausts retries.

Additionally, the `CARGO_TARGET_DIR` fix (commit `b7cbbc3a`) that prevents EXDEV (cross-device link) errors only applies to the codex backend path. Non-codex CLI backends and tool nodes that invoke cargo also hit EXDEV but have no mitigation.

### Why This Is the Right Fix

**Option considered: just fix the skill (make `check_toolchain` a box node).** This would make the check run in the same codex sandbox as downstream work. But it doesn't fix the underlying engine bug — any future graph that mixes tool and codergen nodes will hit the same asymmetry. It also doesn't fix the missing CARGO_TARGET_DIR for non-codex backends.

**Option considered: just fix the engine (preserve toolchain paths in codex env).** This fixes the immediate Rust/cargo case but leaves the fundamental design flaw: tool nodes and codergen nodes have completely independent env construction with no shared contract. The next toolchain (Zig, Haskell, Deno, etc.) will hit the same pattern.

**The chosen approach (both engine + skill)** is the idiomatic solution because:

1. **Engine: shared env construction** — a single `buildNodeEnv()` function produces a base environment that both handlers use. Toolchain-related env vars (`RUSTUP_HOME`, `CARGO_HOME`, `GOPATH`, `PATH` entries for these) are always preserved, even when HOME is overridden. `CARGO_TARGET_DIR` is set whenever a worktree is involved, not just for codex. This makes the engine's environment handling a **single code path** instead of three divergent ones.

2. **Skill: toolchain gates match execution context** — the skill generates `check_toolchain` as a `shape=box` codergen node (same handler as downstream work), not a `shape=parallelogram` tool node. This ensures the gate validates the same environment the work will use. The skill also documents *why* this matters so future skill edits don't regress.

This is belt-and-suspenders: the engine guarantees env consistency (so even if the skill used a tool node, it would still work), and the skill generates correct graphs (so even without the engine fix, the gate validates the right env).

---

### Task 1: Extract `buildBaseNodeEnv` and preserve toolchain paths in codex isolation

This task creates a shared env construction function and fixes `buildCodexIsolatedEnvWithName` to preserve toolchain-related paths.

**Files:**
- Create: `internal/attractor/engine/node_env.go`
- Modify: `internal/attractor/engine/codergen_router.go:890-904` (codex env path)
- Modify: `internal/attractor/engine/codergen_router.go:994-996` (non-codex env path)
- Modify: `internal/attractor/engine/codergen_router.go:1312-1363` (`buildCodexIsolatedEnvWithName`)
- Test: `internal/attractor/engine/node_env_test.go`

**Step 1: Write the failing test for `buildBaseNodeEnv`**

Create `internal/attractor/engine/node_env_test.go`:

```go
package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildBaseNodeEnv_PreservesToolchainPaths(t *testing.T) {
	home := t.TempDir()
	cargoHome := filepath.Join(home, ".cargo")
	rustupHome := filepath.Join(home, ".rustup")
	gopath := filepath.Join(home, "go")

	t.Setenv("HOME", home)
	t.Setenv("CARGO_HOME", cargoHome)
	t.Setenv("RUSTUP_HOME", rustupHome)
	t.Setenv("GOPATH", gopath)

	worktree := t.TempDir()
	env := buildBaseNodeEnv(worktree)

	if got := envLookup(env, "CARGO_HOME"); got != cargoHome {
		t.Fatalf("CARGO_HOME: got %q want %q", got, cargoHome)
	}
	if got := envLookup(env, "RUSTUP_HOME"); got != rustupHome {
		t.Fatalf("RUSTUP_HOME: got %q want %q", got, rustupHome)
	}
	if got := envLookup(env, "GOPATH"); got != gopath {
		t.Fatalf("GOPATH: got %q want %q", got, gopath)
	}
	if got := envLookup(env, "CARGO_TARGET_DIR"); got != filepath.Join(worktree, ".cargo-target") {
		t.Fatalf("CARGO_TARGET_DIR: got %q want %q", got, filepath.Join(worktree, ".cargo-target"))
	}
}

func TestBuildBaseNodeEnv_SetsCargoTargetDirToWorktree(t *testing.T) {
	worktree := t.TempDir()
	env := buildBaseNodeEnv(worktree)

	got := envLookup(env, "CARGO_TARGET_DIR")
	want := filepath.Join(worktree, ".cargo-target")
	if got != want {
		t.Fatalf("CARGO_TARGET_DIR: got %q want %q", got, want)
	}
}

func TestBuildBaseNodeEnv_DoesNotOverrideExplicitCargoTargetDir(t *testing.T) {
	t.Setenv("CARGO_TARGET_DIR", "/custom/target")
	worktree := t.TempDir()
	env := buildBaseNodeEnv(worktree)

	got := envLookup(env, "CARGO_TARGET_DIR")
	if got != "/custom/target" {
		t.Fatalf("CARGO_TARGET_DIR: got %q want %q (should not override explicit)", got, "/custom/target")
	}
}

func TestBuildBaseNodeEnv_InfersToolchainPathsFromHOME(t *testing.T) {
	// When CARGO_HOME/RUSTUP_HOME are not set, they default to $HOME/.cargo and $HOME/.rustup.
	// buildBaseNodeEnv should set them explicitly so downstream HOME overrides don't break them.
	home := t.TempDir()
	t.Setenv("HOME", home)
	os.Unsetenv("CARGO_HOME")
	os.Unsetenv("RUSTUP_HOME")

	worktree := t.TempDir()
	env := buildBaseNodeEnv(worktree)

	if got := envLookup(env, "CARGO_HOME"); got != filepath.Join(home, ".cargo") {
		t.Fatalf("CARGO_HOME: got %q want %q", got, filepath.Join(home, ".cargo"))
	}
	if got := envLookup(env, "RUSTUP_HOME"); got != filepath.Join(home, ".rustup") {
		t.Fatalf("RUSTUP_HOME: got %q want %q", got, filepath.Join(home, ".rustup"))
	}
}

func TestBuildBaseNodeEnv_StripsClaudeCode(t *testing.T) {
	t.Setenv("CLAUDECODE", "1")
	worktree := t.TempDir()
	env := buildBaseNodeEnv(worktree)

	if envHasKey(env, "CLAUDECODE") {
		t.Fatal("CLAUDECODE should be stripped from base env")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/attractor/engine/ -run TestBuildBaseNodeEnv -v -count=1`
Expected: FAIL with "undefined: buildBaseNodeEnv"

**Step 3: Write `buildBaseNodeEnv` implementation**

Create `internal/attractor/engine/node_env.go`:

```go
package engine

import (
	"os"
	"path/filepath"
	"strings"
)

// toolchainEnvKeys are environment variables that locate build toolchains
// (Rust, Go, etc.) relative to HOME. When a handler overrides HOME (e.g.,
// codex isolation), these must be pinned to their original absolute values
// so toolchains remain discoverable.
var toolchainEnvKeys = []string{
	"CARGO_HOME",   // Rust: defaults to $HOME/.cargo
	"RUSTUP_HOME",  // Rust: defaults to $HOME/.rustup
	"GOPATH",       // Go: defaults to $HOME/go
	"GOMODCACHE",   // Go: defaults to $GOPATH/pkg/mod
}

// toolchainDefaults maps env keys to their default relative-to-HOME paths.
// If the key is not set in the environment, buildBaseNodeEnv pins it to
// $HOME/<default> so that later HOME overrides don't break toolchain lookup.
var toolchainDefaults = map[string]string{
	"CARGO_HOME":  ".cargo",
	"RUSTUP_HOME": ".rustup",
}

// buildBaseNodeEnv constructs the base environment for any node execution.
// It:
//   - Starts from os.Environ()
//   - Strips CLAUDECODE (nested session protection)
//   - Pins toolchain paths to absolute values (immune to HOME overrides)
//   - Sets CARGO_TARGET_DIR inside worktree to avoid EXDEV errors
//
// Both ToolHandler and CodergenRouter should use this as their starting env,
// then apply handler-specific overrides on top.
func buildBaseNodeEnv(worktreeDir string) []string {
	base := os.Environ()

	// Snapshot HOME before any overrides.
	home := strings.TrimSpace(os.Getenv("HOME"))

	// Pin toolchain paths to absolute values. If not explicitly set,
	// infer from current HOME so a later HOME override doesn't break them.
	toolchainOverrides := map[string]string{}
	for _, key := range toolchainEnvKeys {
		val := strings.TrimSpace(os.Getenv(key))
		if val != "" {
			// Already set — pin the explicit value.
			toolchainOverrides[key] = val
		} else if defaultRel, ok := toolchainDefaults[key]; ok && home != "" {
			// Not set — pin the default (HOME-relative) path.
			toolchainOverrides[key] = filepath.Join(home, defaultRel)
		}
	}

	// Set CARGO_TARGET_DIR inside the worktree to avoid EXDEV errors
	// when cargo moves intermediate artifacts across filesystem boundaries.
	// Harmless for non-Rust projects (unused env var).
	if worktreeDir != "" && strings.TrimSpace(os.Getenv("CARGO_TARGET_DIR")) == "" {
		toolchainOverrides["CARGO_TARGET_DIR"] = filepath.Join(worktreeDir, ".cargo-target")
	}

	env := mergeEnvWithOverrides(base, toolchainOverrides)

	// Strip CLAUDECODE — it prevents the Claude CLI from launching
	// (nested session protection). All handler types need this stripped.
	return stripEnvKey(env, "CLAUDECODE")
}

// stripEnvKey removes all entries with the given key from an env slice.
func stripEnvKey(env []string, key string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env))
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) || entry == key {
			continue
		}
		out = append(out, entry)
	}
	return out
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/attractor/engine/ -run TestBuildBaseNodeEnv -v -count=1`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/attractor/engine/node_env.go internal/attractor/engine/node_env_test.go
git commit -m "feat(engine): add buildBaseNodeEnv for unified toolchain env handling

Extracts a shared function that both ToolHandler and CodergenRouter
will use as their base environment. Pins CARGO_HOME, RUSTUP_HOME,
GOPATH to absolute values so codex HOME overrides don't break
toolchain discovery. Sets CARGO_TARGET_DIR inside worktree for all
handlers (not just codex). Strips CLAUDECODE universally."
```

---

### Task 2: Wire `ToolHandler` to use `buildBaseNodeEnv`

Currently `ToolHandler.Execute` never sets `cmd.Env`, so it inherits the raw parent process environment. This means tool nodes see the host env (including CLAUDECODE) while codergen nodes see a different env. Wire it to use `buildBaseNodeEnv`.

**Files:**
- Modify: `internal/attractor/engine/handlers.go:440-474` (ToolHandler.Execute)
- Test: `internal/attractor/engine/node_env_test.go` (add integration-style test)

**Step 1: Write the failing test**

Add to `internal/attractor/engine/node_env_test.go`:

```go
func TestToolHandler_UsesBaseNodeEnv(t *testing.T) {
	// A tool node should see pinned toolchain env vars and have CLAUDECODE stripped.
	// We can verify by running a tool_command that echoes env vars.
	t.Setenv("CLAUDECODE", "1")
	worktree := t.TempDir()

	dot := `digraph G {
  graph [goal="test"]
  start [shape=Mdiamond]
  exit [shape=Msquare]
  check [shape=parallelogram, tool_command="bash -c 'echo CLAUDECODE=$CLAUDECODE; echo CARGO_TARGET_DIR=$CARGO_TARGET_DIR'"]
  start -> check -> exit
}`
	e := newTestEngine(t, dot, worktree)
	result, err := e.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %s: %s", result.Status, result.FailureReason)
	}

	// Read stdout to verify env was set correctly.
	stdout, err := os.ReadFile(filepath.Join(e.Options.LogsRoot, "check", "stdout.log"))
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	output := string(stdout)
	if strings.Contains(output, "CLAUDECODE=1") {
		t.Fatal("CLAUDECODE should be stripped from tool node env")
	}
	if !strings.Contains(output, "CARGO_TARGET_DIR=") {
		t.Fatal("CARGO_TARGET_DIR should be set in tool node env")
	}
	cargoTargetDir := filepath.Join(worktree, ".cargo-target")
	if !strings.Contains(output, "CARGO_TARGET_DIR="+cargoTargetDir) {
		t.Fatalf("CARGO_TARGET_DIR should point to worktree, got: %s", output)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/attractor/engine/ -run TestToolHandler_UsesBaseNodeEnv -v -count=1`
Expected: FAIL — CLAUDECODE=1 will appear in output (tool handler inherits parent env)

**Step 3: Wire ToolHandler to use `buildBaseNodeEnv`**

In `internal/attractor/engine/handlers.go`, in `ToolHandler.Execute`, add `cmd.Env` after `cmd.Dir`:

Find (around line 454-456):
```go
	cmd := exec.CommandContext(cctx, "bash", "-c", cmdStr)
	cmd.Dir = execCtx.WorktreeDir
	// Avoid hanging on interactive reads; tool_command doesn't provide a way to supply stdin.
	cmd.Stdin = strings.NewReader("")
```

Replace with:
```go
	cmd := exec.CommandContext(cctx, "bash", "-c", cmdStr)
	cmd.Dir = execCtx.WorktreeDir
	cmd.Env = buildBaseNodeEnv(execCtx.WorktreeDir)
	// Avoid hanging on interactive reads; tool_command doesn't provide a way to supply stdin.
	cmd.Stdin = strings.NewReader("")
```

Also update the `env_mode` in the `tool_invocation.json` write (around line 447) from `"inherit"` to `"base"`:

Find:
```go
		"env_mode":    "inherit",
```

Replace with:
```go
		"env_mode":    "base",
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/attractor/engine/ -run TestToolHandler_UsesBaseNodeEnv -v -count=1`
Expected: PASS

**Step 5: Run full test suite to check for regressions**

Run: `go test ./internal/attractor/engine/ -count=1 -timeout 300s`
Expected: PASS (all existing tool node tests should still pass since they only use simple commands)

**Step 6: Commit**

```bash
git add internal/attractor/engine/handlers.go internal/attractor/engine/node_env_test.go
git commit -m "fix(engine): wire ToolHandler to use buildBaseNodeEnv

Tool nodes now use the same base environment as codergen nodes instead
of inheriting the raw parent process env. This ensures toolchain paths
are pinned, CARGO_TARGET_DIR is set, and CLAUDECODE is stripped.
Fixes the environment asymmetry where check_toolchain (tool node)
validated a different env than implement (codergen node)."
```

---

### Task 3: Wire `CodergenRouter` to use `buildBaseNodeEnv` as its base

The codergen router currently has two separate env construction paths (codex isolated vs. scrubbed inherit). Both should start from `buildBaseNodeEnv` so toolchain paths and CLAUDECODE stripping are handled uniformly.

**Files:**
- Modify: `internal/attractor/engine/codergen_router.go:886-905` (codex path)
- Modify: `internal/attractor/engine/codergen_router.go:991-997` (non-codex path)
- Modify: `internal/attractor/engine/codergen_router.go:1312-1363` (`buildCodexIsolatedEnvWithName`)
- Test: `internal/attractor/engine/node_env_test.go`

**Step 1: Write the failing test**

Add to `internal/attractor/engine/node_env_test.go`:

```go
func TestBuildCodexIsolatedEnv_PreservesToolchainPaths(t *testing.T) {
	home := t.TempDir()
	cargoHome := filepath.Join(home, ".cargo")
	rustupHome := filepath.Join(home, ".rustup")

	t.Setenv("HOME", home)
	t.Setenv("CARGO_HOME", cargoHome)
	t.Setenv("RUSTUP_HOME", rustupHome)
	t.Setenv("CLAUDECODE", "1")

	stateBase := filepath.Join(t.TempDir(), "codex-state-base")
	t.Setenv("KILROY_CODEX_STATE_BASE", stateBase)

	stageDir := t.TempDir()
	worktree := t.TempDir()
	env, _, err := buildCodexIsolatedEnvFromBase(stageDir, buildBaseNodeEnv(worktree))
	if err != nil {
		t.Fatalf("buildCodexIsolatedEnvFromBase: %v", err)
	}

	// HOME should be overridden to isolated dir (not the original home).
	if got := envLookup(env, "HOME"); got == home {
		t.Fatalf("HOME should be overridden to isolated dir, got original: %q", got)
	}

	// But toolchain paths should still point to the ORIGINAL home's paths.
	if got := envLookup(env, "CARGO_HOME"); got != cargoHome {
		t.Fatalf("CARGO_HOME: got %q want %q (should survive HOME override)", got, cargoHome)
	}
	if got := envLookup(env, "RUSTUP_HOME"); got != rustupHome {
		t.Fatalf("RUSTUP_HOME: got %q want %q (should survive HOME override)", got, rustupHome)
	}

	// CLAUDECODE should be stripped.
	if envHasKey(env, "CLAUDECODE") {
		t.Fatal("CLAUDECODE should be stripped")
	}

	// CARGO_TARGET_DIR should be set (from buildBaseNodeEnv).
	if !envHasKey(env, "CARGO_TARGET_DIR") {
		t.Fatal("CARGO_TARGET_DIR should be set")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/attractor/engine/ -run TestBuildCodexIsolatedEnv_PreservesToolchainPaths -v -count=1`
Expected: FAIL — `buildCodexIsolatedEnvFromBase` doesn't exist yet

**Step 3: Refactor `buildCodexIsolatedEnvWithName` to accept a base env**

The key change: instead of calling `os.Environ()` and applying codex-specific overrides, `buildCodexIsolatedEnvWithName` should accept a pre-built base env (from `buildBaseNodeEnv`) and apply codex-specific overrides on top.

In `internal/attractor/engine/codergen_router.go`, refactor `buildCodexIsolatedEnvWithName` (lines 1312-1363):

```go
// buildCodexIsolatedEnvFromBase applies codex-specific HOME/XDG isolation on top of
// the provided base environment (which should come from buildBaseNodeEnv).
func buildCodexIsolatedEnvFromBase(stageDir string, baseEnv []string) ([]string, map[string]any, error) {
	return buildCodexIsolatedEnvFromBaseWithName(stageDir, "codex-home", baseEnv)
}

func buildCodexIsolatedEnvFromBaseWithName(stageDir string, homeDirName string, baseEnv []string) ([]string, map[string]any, error) {
	codexHome, err := codexIsolatedHomeDir(stageDir, homeDirName)
	if err != nil {
		return nil, nil, err
	}
	codexStateRoot := filepath.Join(codexHome, ".codex")
	xdgConfigHome := filepath.Join(codexHome, ".config")
	xdgDataHome := filepath.Join(codexHome, ".local", "share")
	xdgStateHome := filepath.Join(codexHome, ".local", "state")

	for _, dir := range []string{codexHome, codexStateRoot, xdgConfigHome, xdgDataHome, xdgStateHome} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, nil, err
		}
	}

	seeded := []string{}
	seedErrors := []string{}
	// Use the ORIGINAL home (from base env) for seeding codex config files.
	srcHome := envLookupSlice(baseEnv, "ORIGINAL_HOME")
	if srcHome == "" {
		srcHome = strings.TrimSpace(os.Getenv("HOME"))
	}
	if srcHome != "" {
		for _, name := range []string{"auth.json", "config.toml"} {
			src := filepath.Join(srcHome, ".codex", name)
			dst := filepath.Join(codexStateRoot, name)
			copied, err := copyIfExists(src, dst)
			if err != nil {
				seedErrors = append(seedErrors, fmt.Sprintf("%s: %v", name, err))
				continue
			}
			if copied {
				seeded = append(seeded, dst)
			}
		}
	}

	// Apply codex-specific overrides on top of the base env.
	// Toolchain paths (CARGO_HOME, RUSTUP_HOME, etc.) are already pinned
	// in baseEnv by buildBaseNodeEnv, so they survive this HOME override.
	env := mergeEnvWithOverrides(baseEnv, map[string]string{
		"HOME":            codexHome,
		"CODEX_HOME":      codexStateRoot,
		"XDG_CONFIG_HOME": xdgConfigHome,
		"XDG_DATA_HOME":   xdgDataHome,
		"XDG_STATE_HOME":  xdgStateHome,
	})

	meta := map[string]any{
		"state_base_root":  codexStateBaseRoot(),
		"state_root":       codexStateRoot,
		"env_seeded_files": seeded,
	}
	if len(seedErrors) > 0 {
		meta["env_seed_errors"] = seedErrors
	}
	return env, meta, nil
}
```

Note: `envLookupSlice` is a helper to look up a key in an `[]string` env slice (similar to `envLookup` used in tests — check if it already exists or add it).

Then update the callsite in `runCLI` (around line 890-905):

```go
if codexSemantics {
	var err error
	baseEnv := buildBaseNodeEnv(execCtx.WorktreeDir)
	isolatedEnv, isolatedMeta, err = buildCodexIsolatedEnvFromBase(stageDir, baseEnv)
	if err != nil {
		return "", classifiedFailure(err, ""), nil
	}
	// CARGO_TARGET_DIR is already set by buildBaseNodeEnv — no need for
	// the duplicate check that was here before.
}
```

And the non-codex path (around line 994-996):

```go
} else {
	baseEnv := buildBaseNodeEnv(execCtx.WorktreeDir)
	scrubbed := scrubConflictingProviderEnvKeys(baseEnv, providerKey)
	cmd.Env = mergeEnvWithOverrides(scrubbed, contract.EnvVars)
}
```

Keep the old `buildCodexIsolatedEnv` and `buildCodexIsolatedEnvWithName` functions as thin wrappers that call the new `FromBase` variants (to avoid breaking any other callers or tests), OR update all callers. Check for callers with `grep -rn buildCodexIsolatedEnv` first.

**Step 4: Run test to verify it passes**

Run: `go test ./internal/attractor/engine/ -run TestBuildCodexIsolatedEnv_PreservesToolchainPaths -v -count=1`
Expected: PASS

**Step 5: Run existing codex isolation test to verify no regression**

Run: `go test ./internal/attractor/engine/ -run TestBuildCodexIsolatedEnv -v -count=1`
Expected: PASS (both old and new tests)

**Step 6: Run full test suite**

Run: `go test ./internal/attractor/engine/ -count=1 -timeout 300s`
Expected: PASS

**Step 7: Commit**

```bash
git add internal/attractor/engine/codergen_router.go internal/attractor/engine/node_env.go internal/attractor/engine/node_env_test.go
git commit -m "fix(engine): wire CodergenRouter to use buildBaseNodeEnv

Both codex and non-codex CLI backends now start from buildBaseNodeEnv,
ensuring toolchain paths survive codex HOME isolation. Removes the
duplicate CARGO_TARGET_DIR logic from the codex-specific path since
buildBaseNodeEnv handles it for all handlers.

The codex isolation now layers on top of the base env:
  os.Environ() -> buildBaseNodeEnv (pin toolchains, strip CLAUDECODE,
  set CARGO_TARGET_DIR) -> buildCodexIsolatedEnvFromBase (override
  HOME, XDG_*, seed codex config files)"
```

---

### Task 4: Update the english-to-dotfile skill to generate toolchain gates correctly

The skill's Phase 3 "Toolchain bootstrap" section instructs generating `check_toolchain` as `shape=parallelogram` (tool handler). This should be `shape=box` (codergen handler) so the gate runs in the same execution context as downstream work nodes.

**Files:**
- Modify: `skills/english-to-dotfile/SKILL.md` (3 locations)

**Step 1: Update the toolchain bootstrap section (around lines 398-408)**

Find the example DOT readiness gate:
```dot
check_toolchain [
    shape=parallelogram,
    max_retries=0,
    tool_command="bash -lc 'set -euo pipefail; command -v cargo >/dev/null || { echo \"missing required tool: cargo\" >&2; exit 1; }; command -v wasm-pack >/dev/null || { echo \"missing required tool: wasm-pack\" >&2; exit 1; }'"
]
```

Replace with:
```dot
check_toolchain [
    shape=box,
    max_retries=0,
    prompt="Check that all required build tools are installed and working.\n\nRun:\n  command -v cargo || { echo 'missing: cargo — install from https://rustup.rs' >&2; exit 1; }\n  command -v wasm-pack || { echo 'missing: wasm-pack — install with: cargo install wasm-pack' >&2; exit 1; }\n  rustup target list --installed | grep -q wasm32-unknown-unknown || { echo 'missing: wasm32-unknown-unknown target — install with: rustup target add wasm32-unknown-unknown' >&2; exit 1; }\n\nWrite status JSON to $KILROY_STAGE_STATUS_PATH (absolute path), fallback to $KILROY_STAGE_STATUS_FALLBACK_PATH, and do not write nested status.json files.\nWrite status JSON: outcome=success if all tools found, outcome=fail with failure_reason=toolchain_missing and details listing missing tools."
]
```

**Step 2: Update the description text (around line 368)**

Find:
```
3. Always add an early DOT readiness gate (`shape=parallelogram` `tool_command`) to fail fast with actionable errors.
```

Replace with:
```
3. Always add an early DOT readiness gate (`shape=box` codergen node) to fail fast with actionable errors. Use `shape=box` (not `shape=parallelogram`) so the gate runs in the same execution environment as downstream work nodes — a `shape=parallelogram` tool node inherits the host environment, which may differ from the codex sandbox environment where implementation nodes execute.
```

**Step 3: Update anti-pattern #20 (around line 958)**

Find:
```
20. **Missing toolchain readiness gates for non-default build dependencies.** If the deliverable needs tools that are often absent (for example `wasm-pack`, Playwright browsers, mobile SDKs), add an early `shape=parallelogram` tool node that checks prerequisites and blocks the pipeline before expensive LLM stages.
```

Replace with:
```
20. **Missing toolchain readiness gates for non-default build dependencies.** If the deliverable needs tools that are often absent (for example `wasm-pack`, Playwright browsers, mobile SDKs), add an early `shape=box` codergen node that checks prerequisites and blocks the pipeline before expensive LLM stages. Use `shape=box` (not `shape=parallelogram`) so the gate validates the same execution environment as downstream work nodes.
```

**Step 4: Add new anti-pattern #33**

After anti-pattern #32, add:

```
33. **Toolchain gates as tool nodes (`shape=parallelogram`).** Do NOT use `shape=parallelogram` for toolchain readiness checks. Tool nodes inherit the host process environment, but codergen nodes (where the actual work happens) may run in an isolated environment with different HOME/PATH. A `check_toolchain` tool node can find `cargo` on the host while the downstream `implement` codex node can't. Use `shape=box` for toolchain gates so they validate the same environment the work will use.
```

**Step 5: Update DSL quick reference table if needed**

Check the `parallelogram` row in the DSL table. It currently says:
```
| `parallelogram` | tool | Shell command execution (uses `tool_command` attribute). |
```

Add a note. Find the parallelogram row and update to:
```
| `parallelogram` | tool | Shell command execution (uses `tool_command` attribute). **Not for toolchain checks** — use `box` instead so the check runs in the same env as downstream codergen nodes. |
```

**Step 6: Commit**

```bash
git add skills/english-to-dotfile/SKILL.md
git commit -m "fix(skill): generate toolchain gates as box nodes, not parallelogram

Tool nodes (shape=parallelogram) inherit the host process env, but
codergen nodes (shape=box) may run in an isolated codex sandbox with
a different HOME. This caused check_toolchain to pass while downstream
implement/verify failed with rust_toolchain_unavailable.

Changes:
- Toolchain bootstrap example: shape=parallelogram -> shape=box
- Anti-pattern #20: updated to recommend shape=box
- Anti-pattern #33: new, warns against parallelogram toolchain gates
- DSL quick reference: note on parallelogram row"
```

---

### Task 5: Run full test suite and verify end-to-end

**Step 1: Run full engine tests**

Run: `go test ./internal/attractor/engine/ -count=1 -timeout 300s`
Expected: PASS

**Step 2: Run validator on existing dotfiles to check for regressions**

Run:
```bash
go run ./cmd/kilroy attractor validate --graph demo/rogue/rogue.dot
go run ./cmd/kilroy attractor validate --graph docs/strongdm/dot\ specs/consensus_task.dot
```
Expected: Both pass (or same warnings as before — no new errors)

**Step 3: Verify no references to old pattern remain in skill**

Run: `grep -n 'shape=parallelogram.*tool_command.*command -v' skills/english-to-dotfile/SKILL.md`
Expected: No matches (the old pattern is replaced)

**Step 4: Verify new env function is used by both handlers**

Run: `grep -n 'buildBaseNodeEnv' internal/attractor/engine/handlers.go internal/attractor/engine/codergen_router.go`
Expected: Both files reference `buildBaseNodeEnv`

**Step 5: Commit (if any fixups needed)**

Only if fixes were needed. Otherwise this task is just verification.

---

## Verification Checklist

- [ ] `buildBaseNodeEnv` is called by ToolHandler (handlers.go)
- [ ] `buildBaseNodeEnv` is called by CodergenRouter for both codex and non-codex paths
- [ ] CARGO_HOME, RUSTUP_HOME pinned to absolute values in all handler types
- [ ] CARGO_TARGET_DIR set for all handler types (not just codex)
- [ ] CLAUDECODE stripped for all handler types (not just via `conflictingProviderEnvKeys`)
- [ ] Tool nodes log `env_mode: "base"` (not `"inherit"`)
- [ ] Skill generates `check_toolchain` as `shape=box`, not `shape=parallelogram`
- [ ] Anti-pattern #33 documents the pitfall
- [ ] All existing tests pass
- [ ] New tests cover: toolchain preservation, CARGO_TARGET_DIR, CLAUDECODE stripping, codex HOME override doesn't break toolchain paths
