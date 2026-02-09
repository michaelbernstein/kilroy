package engine

import (
	"strings"
	"testing"

	"github.com/strongdm/kilroy/internal/providerspec"
)

func TestResolveProviderExecutable_RealRejectsEnvOverrides(t *testing.T) {
	t.Setenv("KILROY_CODEX_PATH", "/tmp/fake/codex")

	cfg := &RunConfigFile{}
	cfg.LLM.CLIProfile = "real"
	cfg.LLM.Providers = map[string]ProviderConfig{
		"openai": {Backend: BackendCLI},
	}

	_, err := ResolveProviderExecutable(cfg, "openai", RunOptions{})
	if err == nil {
		t.Fatalf("expected error when env override is present in real profile")
	}
	if !strings.Contains(err.Error(), "KILROY_CODEX_PATH") {
		t.Fatalf("expected env key in error, got %v", err)
	}
}

func TestResolveProviderExecutable_RealReturnsCanonicalDefaults(t *testing.T) {
	cfg := &RunConfigFile{}
	cfg.LLM.CLIProfile = "real"
	cfg.LLM.Providers = map[string]ProviderConfig{
		"openai":    {Backend: BackendCLI},
		"anthropic": {Backend: BackendCLI},
		"google":    {Backend: BackendCLI},
	}

	tests := []struct {
		provider string
		wantExe  string
	}{
		{provider: "openai", wantExe: "codex"},
		{provider: "anthropic", wantExe: "claude"},
		{provider: "google", wantExe: "gemini"},
	}

	for _, tc := range tests {
		t.Run(tc.provider, func(t *testing.T) {
			got, err := ResolveProviderExecutable(cfg, tc.provider, RunOptions{})
			if err != nil {
				t.Fatalf("ResolveProviderExecutable: %v", err)
			}
			if got != tc.wantExe {
				t.Fatalf("executable=%q want %q", got, tc.wantExe)
			}
		})
	}
}

func TestResolveProviderExecutable_TestShimRequiresAllowFlag(t *testing.T) {
	cfg := &RunConfigFile{}
	cfg.LLM.CLIProfile = "test_shim"
	cfg.LLM.Providers = map[string]ProviderConfig{
		"openai": {Backend: BackendCLI, Executable: "/tmp/fake/codex"},
	}

	_, err := ResolveProviderExecutable(cfg, "openai", RunOptions{})
	if err == nil {
		t.Fatalf("expected allow-test-shim error")
	}
	if !strings.Contains(err.Error(), "--allow-test-shim") {
		t.Fatalf("expected remediation in error, got %v", err)
	}
}

func TestResolveProviderExecutable_TestShimRequiresExplicitExecutable(t *testing.T) {
	cfg := &RunConfigFile{}
	cfg.LLM.CLIProfile = "test_shim"
	cfg.LLM.Providers = map[string]ProviderConfig{
		"openai": {Backend: BackendCLI},
	}

	_, err := ResolveProviderExecutable(cfg, "openai", RunOptions{AllowTestShim: true})
	if err == nil {
		t.Fatalf("expected explicit executable error")
	}
	if !strings.Contains(err.Error(), "llm.providers.openai.executable") {
		t.Fatalf("expected executable config key in error, got %v", err)
	}
}

func TestDefaultCLIInvocation_UsesSpecTemplate(t *testing.T) {
	spec := providerspec.CLISpec{
		DefaultExecutable:  "mycli",
		InvocationTemplate: []string{"run", "--model", "{{model}}", "--cwd", "{{worktree}}", "--prompt", "{{prompt}}"},
	}
	exe, args := materializeCLIInvocation(spec, "m1", "/tmp/w", "fix bug")
	if exe != "mycli" || strings.Join(args, " ") != "run --model m1 --cwd /tmp/w --prompt fix bug" {
		t.Fatalf("materialization mismatch: exe=%s args=%v", exe, args)
	}
}
