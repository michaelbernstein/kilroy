package engine

import (
	"context"
	"fmt"
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

func TestBuildBaseNodeEnv_InfersGoPathsFromHOME(t *testing.T) {
	// When GOPATH/GOMODCACHE are not set, Go defaults them to $HOME/go
	// and $HOME/go/pkg/mod. buildBaseNodeEnv should pin them explicitly
	// so that later HOME overrides (codex isolation) don't break Go
	// toolchain resolution.
	home := t.TempDir()
	t.Setenv("HOME", home)
	os.Unsetenv("GOPATH")
	os.Unsetenv("GOMODCACHE")

	worktree := t.TempDir()
	env := buildBaseNodeEnv(worktree)

	if got := envLookup(env, "GOPATH"); got != filepath.Join(home, "go") {
		t.Fatalf("GOPATH: got %q want %q", got, filepath.Join(home, "go"))
	}
	if got := envLookup(env, "GOMODCACHE"); got != filepath.Join(home, "go", "pkg", "mod") {
		t.Fatalf("GOMODCACHE: got %q want %q", got, filepath.Join(home, "go", "pkg", "mod"))
	}
}

func TestBuildBaseNodeEnv_GoModCacheUsesFirstGOPATHEntry(t *testing.T) {
	// GOPATH can be a colon-separated list. Go uses the first entry
	// for GOMODCACHE ($GOPATH[0]/pkg/mod). Verify we match that behavior.
	first := t.TempDir()
	second := t.TempDir()
	multiPath := first + string(filepath.ListSeparator) + second
	t.Setenv("GOPATH", multiPath)
	os.Unsetenv("GOMODCACHE")

	worktree := t.TempDir()
	env := buildBaseNodeEnv(worktree)

	// GOPATH should be pinned as-is (the full list).
	if got := envLookup(env, "GOPATH"); got != multiPath {
		t.Fatalf("GOPATH: got %q want %q", got, multiPath)
	}
	// GOMODCACHE should use only the first entry.
	want := filepath.Join(first, "pkg", "mod")
	if got := envLookup(env, "GOMODCACHE"); got != want {
		t.Fatalf("GOMODCACHE: got %q want %q (should use first GOPATH entry)", got, want)
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

func TestToolHandler_UsesBaseNodeEnv(t *testing.T) {
	// A tool node should see pinned toolchain env vars and have CLAUDECODE stripped.
	// We can verify by running a tool_command that echoes env vars.
	t.Setenv("CLAUDECODE", "1")

	dot := []byte(`digraph G {
  graph [goal="test"]
  start [shape=Mdiamond]
  exit [shape=Msquare]
  check [shape=parallelogram, tool_command="bash -c 'echo CLAUDECODE=$CLAUDECODE; echo CARGO_TARGET_DIR=$CARGO_TARGET_DIR'"]
  start -> check -> exit
}`)
	repo := initTestRepo(t)
	logsRoot := t.TempDir()
	result, err := Run(context.Background(), dot, RunOptions{RepoPath: repo, LogsRoot: logsRoot})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.FinalStatus != "success" {
		t.Fatalf("expected success, got %s", result.FinalStatus)
	}

	// Read stdout to verify env was set correctly.
	stdout, err := os.ReadFile(filepath.Join(logsRoot, "check", "stdout.log"))
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	output := string(stdout)
	if strings.Contains(output, "CLAUDECODE=1") {
		t.Fatal("CLAUDECODE should be stripped from tool node env")
	}
	// Check CARGO_TARGET_DIR is set to a non-empty absolute path.
	// Note: "echo CARGO_TARGET_DIR=$CARGO_TARGET_DIR" always prints the
	// literal prefix even when empty, so we check for "=/" (absolute path).
	if !strings.Contains(output, "CARGO_TARGET_DIR=/") {
		t.Fatalf("CARGO_TARGET_DIR should be set to an absolute path in tool node env; output: %s", output)
	}
}

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
	env, _, err := buildCodexIsolatedEnv(stageDir, buildBaseNodeEnv(worktree))
	if err != nil {
		t.Fatalf("buildCodexIsolatedEnv: %v", err)
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

func TestBuildCodexIsolatedEnvWithName_RetryPreservesToolchainPaths(t *testing.T) {
	// Regression test: retry-rebuilt codex envs must preserve toolchain
	// paths. This is the highest-risk path — state-DB and timeout retries
	// rebuild the env on each attempt. If they don't receive baseEnv,
	// CARGO_TARGET_DIR and toolchain paths are silently dropped.
	home := t.TempDir()
	cargoHome := filepath.Join(home, ".cargo")
	rustupHome := filepath.Join(home, ".rustup")

	t.Setenv("HOME", home)
	t.Setenv("CARGO_HOME", cargoHome)
	t.Setenv("RUSTUP_HOME", rustupHome)

	stateBase := filepath.Join(t.TempDir(), "codex-state-base")
	t.Setenv("KILROY_CODEX_STATE_BASE", stateBase)

	stageDir := t.TempDir()
	worktree := t.TempDir()
	baseEnv := buildBaseNodeEnv(worktree)

	// Simulate multiple retry attempts like the real retry loops.
	for attempt := 1; attempt <= 3; attempt++ {
		name := fmt.Sprintf("codex-home-retry%d", attempt)
		env, _, err := buildCodexIsolatedEnvWithName(stageDir, name, baseEnv)
		if err != nil {
			t.Fatalf("attempt %d: buildCodexIsolatedEnvWithName: %v", attempt, err)
		}

		// Each retry must have its own isolated HOME.
		retryHome := envLookup(env, "HOME")
		if retryHome == home {
			t.Fatalf("attempt %d: HOME should be isolated, got original: %q", attempt, retryHome)
		}

		// Toolchain paths must survive every retry rebuild.
		if got := envLookup(env, "CARGO_HOME"); got != cargoHome {
			t.Fatalf("attempt %d: CARGO_HOME: got %q want %q", attempt, got, cargoHome)
		}
		if got := envLookup(env, "RUSTUP_HOME"); got != rustupHome {
			t.Fatalf("attempt %d: RUSTUP_HOME: got %q want %q", attempt, got, rustupHome)
		}
		if !envHasKey(env, "CARGO_TARGET_DIR") {
			t.Fatalf("attempt %d: CARGO_TARGET_DIR should be set", attempt)
		}
	}
}
