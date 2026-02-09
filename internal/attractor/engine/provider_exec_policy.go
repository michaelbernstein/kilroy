package engine

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/strongdm/kilroy/internal/providerspec"
)

const (
	executableSourceDefault          = "default"
	executableSourceConfigExecutable = "config.executable"
)

type providerExecutableResolution struct {
	Executable string
	Source     string
}

// ResolveProviderExecutable applies run-level policy for provider CLI executable selection.
func ResolveProviderExecutable(cfg *RunConfigFile, provider string, opts RunOptions) (string, error) {
	resolution, err := resolveProviderExecutable(cfg, provider, opts)
	if err != nil {
		return "", err
	}
	return resolution.Executable, nil
}

func resolveProviderExecutable(cfg *RunConfigFile, provider string, opts RunOptions) (providerExecutableResolution, error) {
	defaultExe, _, ok := providerDefaultExecutable(provider)
	if !ok {
		return providerExecutableResolution{}, fmt.Errorf("no cli invocation mapping for provider %s", provider)
	}

	profile := normalizedCLIProfile(cfg)
	switch profile {
	case "real":
		if overrides := configuredProviderPathOverrides(); len(overrides) > 0 {
			return providerExecutableResolution{}, fmt.Errorf("llm.cli_profile=real forbids provider path overrides via %s (unset overrides or use llm.cli_profile=test_shim with --allow-test-shim)", strings.Join(overrides, ", "))
		}
		if providerCfg, _, exists := providerConfigFor(cfg, provider); exists && strings.TrimSpace(providerCfg.Executable) != "" {
			return providerExecutableResolution{}, fmt.Errorf("llm.providers.%s.executable is only allowed when llm.cli_profile=test_shim", normalizeProviderKey(provider))
		}
		return providerExecutableResolution{Executable: defaultExe, Source: executableSourceDefault}, nil
	case "test_shim":
		if !opts.AllowTestShim {
			return providerExecutableResolution{}, fmt.Errorf("llm.cli_profile=test_shim requires --allow-test-shim")
		}
		providerCfg, providerKey, exists := providerConfigFor(cfg, provider)
		if !exists || strings.TrimSpace(providerCfg.Executable) == "" {
			return providerExecutableResolution{}, fmt.Errorf("llm.providers.%s.executable is required when llm.cli_profile=test_shim", providerKey)
		}
		return providerExecutableResolution{
			Executable: strings.TrimSpace(providerCfg.Executable),
			Source:     executableSourceConfigExecutable,
		}, nil
	default:
		return providerExecutableResolution{}, fmt.Errorf("invalid llm.cli_profile: %q (want real|test_shim)", profile)
	}
}

func validateRunCLIProfilePolicy(cfg *RunConfigFile, opts RunOptions) error {
	switch normalizedCLIProfile(cfg) {
	case "real":
		if overrides := configuredProviderPathOverrides(); len(overrides) > 0 {
			return fmt.Errorf("preflight: llm.cli_profile=real forbids provider path overrides via %s (unset overrides or use llm.cli_profile=test_shim with --allow-test-shim)", strings.Join(overrides, ", "))
		}
		return nil
	case "test_shim":
		if !opts.AllowTestShim {
			return fmt.Errorf("preflight: llm.cli_profile=test_shim requires --allow-test-shim")
		}
		return nil
	default:
		return fmt.Errorf("invalid llm.cli_profile: %q (want real|test_shim)", normalizedCLIProfile(cfg))
	}
}

func normalizedCLIProfile(cfg *RunConfigFile) string {
	if cfg == nil {
		return "real"
	}
	profile := strings.ToLower(strings.TrimSpace(cfg.LLM.CLIProfile))
	if profile == "" {
		return "real"
	}
	return profile
}

func providerConfigFor(cfg *RunConfigFile, provider string) (ProviderConfig, string, bool) {
	key := normalizeProviderKey(provider)
	if cfg == nil || len(cfg.LLM.Providers) == 0 {
		return ProviderConfig{}, key, false
	}
	for k, v := range cfg.LLM.Providers {
		if normalizeProviderKey(k) == key {
			return v, key, true
		}
	}
	return ProviderConfig{}, key, false
}

func configuredProviderPathOverrides() []string {
	keys := []string{"KILROY_CODEX_PATH", "KILROY_CLAUDE_PATH", "KILROY_GEMINI_PATH"}
	var set []string
	for _, key := range keys {
		if strings.TrimSpace(os.Getenv(key)) != "" {
			set = append(set, key)
		}
	}
	sort.Strings(set)
	return set
}

func providerDefaultExecutable(provider string) (exe string, envKey string, ok bool) {
	spec := defaultCLISpecForProvider(provider)
	if spec == nil {
		return "", "", false
	}
	return strings.TrimSpace(spec.DefaultExecutable), providerPathOverrideEnvKey(provider), true
}

func defaultCLISpecForProvider(provider string) *providerspec.CLISpec {
	key := normalizeProviderKey(provider)
	if key == "" {
		return nil
	}
	builtin, ok := providerspec.Builtin(key)
	if !ok || builtin.CLI == nil {
		return nil
	}
	return cloneCLISpec(builtin.CLI)
}

func providerPathOverrideEnvKey(provider string) string {
	switch normalizeProviderKey(provider) {
	case "openai":
		return "KILROY_CODEX_PATH"
	case "anthropic":
		return "KILROY_CLAUDE_PATH"
	case "google":
		return "KILROY_GEMINI_PATH"
	default:
		return ""
	}
}

func materializeCLIInvocation(spec providerspec.CLISpec, modelID, worktree, prompt string) (string, []string) {
	exe := strings.TrimSpace(spec.DefaultExecutable)
	args := make([]string, 0, len(spec.InvocationTemplate))
	for _, token := range spec.InvocationTemplate {
		repl := token
		switch token {
		case "{{model}}":
			repl = modelID
		case "{{worktree}}":
			repl = worktree
		case "{{prompt}}":
			repl = prompt
		}
		if repl == "" && (token == "{{prompt}}" || token == "{{model}}" || token == "{{worktree}}") {
			continue
		}
		args = append(args, repl)
	}
	return exe, args
}
