package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/strongdm/kilroy/internal/providerspec"

	"gopkg.in/yaml.v3"
)

type BackendKind string

const (
	BackendAPI BackendKind = "api"
	BackendCLI BackendKind = "cli"
)

type ProviderAPIConfig struct {
	Protocol           string            `json:"protocol,omitempty" yaml:"protocol,omitempty"`
	BaseURL            string            `json:"base_url,omitempty" yaml:"base_url,omitempty"`
	Path               string            `json:"path,omitempty" yaml:"path,omitempty"`
	APIKeyEnv          string            `json:"api_key_env,omitempty" yaml:"api_key_env,omitempty"`
	ProviderOptionsKey string            `json:"provider_options_key,omitempty" yaml:"provider_options_key,omitempty"`
	ProfileFamily      string            `json:"profile_family,omitempty" yaml:"profile_family,omitempty"`
	Headers            map[string]string `json:"headers,omitempty" yaml:"headers,omitempty"`
}

type ProviderConfig struct {
	Backend    BackendKind       `json:"backend" yaml:"backend"`
	Executable string            `json:"executable,omitempty" yaml:"executable,omitempty"`
	API        ProviderAPIConfig `json:"api,omitempty" yaml:"api,omitempty"`
	Failover   []string          `json:"failover,omitempty" yaml:"failover,omitempty"`
}

type RunConfigFile struct {
	Version int `json:"version" yaml:"version"`

	Repo struct {
		Path string `json:"path" yaml:"path"`
	} `json:"repo" yaml:"repo"`

	CXDB struct {
		BinaryAddr  string `json:"binary_addr" yaml:"binary_addr"`
		HTTPBaseURL string `json:"http_base_url" yaml:"http_base_url"`
		Autostart   struct {
			Enabled        bool     `json:"enabled" yaml:"enabled"`
			Command        []string `json:"command" yaml:"command"`
			WaitTimeoutMS  int      `json:"wait_timeout_ms" yaml:"wait_timeout_ms"`
			PollIntervalMS int      `json:"poll_interval_ms" yaml:"poll_interval_ms"`
			UI             struct {
				Enabled bool     `json:"enabled" yaml:"enabled"`
				Command []string `json:"command" yaml:"command"`
				URL     string   `json:"url" yaml:"url"`
			} `json:"ui" yaml:"ui"`
		} `json:"autostart" yaml:"autostart"`
	} `json:"cxdb" yaml:"cxdb"`

	LLM struct {
		CLIProfile string                    `json:"cli_profile" yaml:"cli_profile"`
		Providers  map[string]ProviderConfig `json:"providers" yaml:"providers"`
	} `json:"llm" yaml:"llm"`

	ModelDB struct {
		OpenRouterModelInfoPath           string `json:"openrouter_model_info_path" yaml:"openrouter_model_info_path"`
		OpenRouterModelInfoUpdatePolicy   string `json:"openrouter_model_info_update_policy" yaml:"openrouter_model_info_update_policy"`
		OpenRouterModelInfoURL            string `json:"openrouter_model_info_url" yaml:"openrouter_model_info_url"`
		OpenRouterModelInfoFetchTimeoutMS int    `json:"openrouter_model_info_fetch_timeout_ms" yaml:"openrouter_model_info_fetch_timeout_ms"`
	} `json:"modeldb" yaml:"modeldb"`

	Git struct {
		RequireClean    bool   `json:"require_clean" yaml:"require_clean"`
		RunBranchPrefix string `json:"run_branch_prefix" yaml:"run_branch_prefix"`
		CommitPerNode   bool   `json:"commit_per_node" yaml:"commit_per_node"`
	} `json:"git" yaml:"git"`
}

func LoadRunConfigFile(path string) (*RunConfigFile, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg RunConfigFile
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".json":
		if err := json.Unmarshal(b, &cfg); err != nil {
			return nil, err
		}
	default:
		if err := yaml.Unmarshal(b, &cfg); err != nil {
			return nil, err
		}
	}
	applyConfigDefaults(&cfg)
	if err := validateConfig(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func applyConfigDefaults(cfg *RunConfigFile) {
	if cfg == nil {
		return
	}
	if cfg.Version == 0 {
		cfg.Version = 1
	}
	if cfg.Git.RunBranchPrefix == "" {
		cfg.Git.RunBranchPrefix = "attractor/run"
	}
	// metaspec default.
	if !cfg.Git.CommitPerNode {
		cfg.Git.CommitPerNode = true
	}
	// metaspec default.
	if !cfg.Git.RequireClean {
		cfg.Git.RequireClean = true
	}
	if cfg.LLM.Providers == nil {
		cfg.LLM.Providers = map[string]ProviderConfig{}
	}
	if strings.TrimSpace(cfg.LLM.CLIProfile) == "" {
		cfg.LLM.CLIProfile = "real"
	} else {
		cfg.LLM.CLIProfile = strings.ToLower(strings.TrimSpace(cfg.LLM.CLIProfile))
	}
	cfg.ModelDB.OpenRouterModelInfoPath = strings.TrimSpace(cfg.ModelDB.OpenRouterModelInfoPath)
	cfg.ModelDB.OpenRouterModelInfoUpdatePolicy = strings.TrimSpace(cfg.ModelDB.OpenRouterModelInfoUpdatePolicy)
	if cfg.ModelDB.OpenRouterModelInfoUpdatePolicy == "" {
		cfg.ModelDB.OpenRouterModelInfoUpdatePolicy = "on_run_start"
	}
	cfg.ModelDB.OpenRouterModelInfoURL = strings.TrimSpace(cfg.ModelDB.OpenRouterModelInfoURL)
	if cfg.ModelDB.OpenRouterModelInfoURL == "" {
		cfg.ModelDB.OpenRouterModelInfoURL = "https://openrouter.ai/api/v1/models"
	}
	if cfg.ModelDB.OpenRouterModelInfoFetchTimeoutMS == 0 {
		cfg.ModelDB.OpenRouterModelInfoFetchTimeoutMS = 5000
	}
	if cfg.CXDB.Autostart.WaitTimeoutMS == 0 {
		cfg.CXDB.Autostart.WaitTimeoutMS = 20000
	}
	if cfg.CXDB.Autostart.PollIntervalMS == 0 {
		cfg.CXDB.Autostart.PollIntervalMS = 250
	}
	cfg.CXDB.Autostart.Command = trimNonEmpty(cfg.CXDB.Autostart.Command)
	cfg.CXDB.Autostart.UI.Command = trimNonEmpty(cfg.CXDB.Autostart.UI.Command)
	cfg.CXDB.Autostart.UI.URL = strings.TrimSpace(cfg.CXDB.Autostart.UI.URL)
}

func validateConfig(cfg *RunConfigFile) error {
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}
	if cfg.Version != 1 {
		return fmt.Errorf("unsupported config version: %d", cfg.Version)
	}
	if strings.TrimSpace(cfg.Repo.Path) == "" {
		return fmt.Errorf("repo.path is required")
	}
	if strings.TrimSpace(cfg.CXDB.BinaryAddr) == "" || strings.TrimSpace(cfg.CXDB.HTTPBaseURL) == "" {
		return fmt.Errorf("cxdb.binary_addr and cxdb.http_base_url are required in v1")
	}
	if cfg.CXDB.Autostart.WaitTimeoutMS < 0 {
		return fmt.Errorf("cxdb.autostart.wait_timeout_ms must be >= 0")
	}
	if cfg.CXDB.Autostart.PollIntervalMS < 0 {
		return fmt.Errorf("cxdb.autostart.poll_interval_ms must be >= 0")
	}
	if cfg.CXDB.Autostart.Enabled && len(cfg.CXDB.Autostart.Command) == 0 {
		return fmt.Errorf("cxdb.autostart.command is required when cxdb.autostart.enabled=true")
	}
	if strings.TrimSpace(cfg.ModelDB.OpenRouterModelInfoPath) == "" {
		return fmt.Errorf("modeldb.openrouter_model_info_path is required")
	}
	switch strings.ToLower(strings.TrimSpace(cfg.ModelDB.OpenRouterModelInfoUpdatePolicy)) {
	case "pinned", "on_run_start":
		// ok
	default:
		return fmt.Errorf("invalid modeldb.openrouter_model_info_update_policy: %q (want pinned|on_run_start)", cfg.ModelDB.OpenRouterModelInfoUpdatePolicy)
	}
	if strings.ToLower(strings.TrimSpace(cfg.ModelDB.OpenRouterModelInfoUpdatePolicy)) == "on_run_start" && strings.TrimSpace(cfg.ModelDB.OpenRouterModelInfoURL) == "" {
		return fmt.Errorf("modeldb.openrouter_model_info_url is required when update_policy=on_run_start")
	}
	switch strings.ToLower(strings.TrimSpace(cfg.LLM.CLIProfile)) {
	case "real", "test_shim":
		// ok
	default:
		return fmt.Errorf("invalid llm.cli_profile: %q (want real|test_shim)", cfg.LLM.CLIProfile)
	}
	for prov, pc := range cfg.LLM.Providers {
		canonical := providerspec.CanonicalProviderKey(prov)
		builtin, hasBuiltin := providerspec.Builtin(canonical)
		switch pc.Backend {
		case BackendAPI:
			protocol := strings.TrimSpace(pc.API.Protocol)
			if protocol == "" && hasBuiltin && builtin.API != nil {
				protocol = string(builtin.API.Protocol)
			}
			if protocol == "" {
				return fmt.Errorf("llm.providers.%s.api.protocol is required for api backend", prov)
			}
		case BackendCLI:
			if !hasBuiltin || builtin.CLI == nil {
				return fmt.Errorf("llm.providers.%s backend=cli requires builtin provider with cli contract", prov)
			}
		default:
			return fmt.Errorf("invalid backend for provider %q: %q (want api|cli)", prov, pc.Backend)
		}
		if strings.EqualFold(cfg.LLM.CLIProfile, "real") && strings.TrimSpace(pc.Executable) != "" {
			return fmt.Errorf("llm.providers.%s.executable is only allowed when llm.cli_profile=test_shim", prov)
		}
	}
	return nil
}

func normalizeProviderKey(k string) string {
	return providerspec.CanonicalProviderKey(k)
}

func trimNonEmpty(parts []string) []string {
	if len(parts) == 0 {
		return nil
	}
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}
