# Provider Plug-in Refactor + Kimi/Z API Support Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Replace hard-coded provider branching with a provider plug-in architecture so Kimi and Z are supported via API immediately and new providers can be added with config + protocol selection rather than engine code edits.

**Architecture:** Add a provider-spec registry (built-in defaults plus run-config overrides), refactor API/CLI routing to consume runtime provider definitions, and select adapters by API protocol family instead of provider name. Keep backward compatibility for `openai`, `anthropic`, and `google`, while adding built-in `kimi` and `zai` API providers. Move agent profile/failover/CLI contracts to data-driven metadata.

**Tech Stack:** Go, YAML (`gopkg.in/yaml.v3`), JSON, `net/http`, existing Kilroy engine/LLM packages, `go test`.

---

### Task 1: Create Provider Spec Registry Core (Single Canonicalization Source)

**Files:**
- Create: `internal/providerspec/spec.go`
- Create: `internal/providerspec/builtin.go`
- Test: `internal/providerspec/spec_test.go`
- Modify: `internal/attractor/engine/config.go`
- Modify: `internal/llm/client.go`
- Test: `internal/attractor/engine/config_test.go`
- Test: `internal/llm/client_test.go`

**Step 1: Write the failing test**

```go
package providerspec

import "testing"

func TestBuiltinSpecsIncludeCoreAndNewProviders(t *testing.T) {
	s := Builtins()
	for _, key := range []string{"openai", "anthropic", "google", "kimi", "zai"} {
		if _, ok := s[key]; !ok {
			t.Fatalf("missing builtin provider %q", key)
		}
	}
}

func TestCanonicalProviderKey_Aliases(t *testing.T) {
	if got := CanonicalProviderKey("gemini"); got != "google" {
		t.Fatalf("gemini alias: got %q want %q", got, "google")
	}
	if got := CanonicalProviderKey(" Z-AI "); got != "zai" {
		t.Fatalf("z-ai alias: got %q want %q", got, "zai")
	}
	if got := CanonicalProviderKey("moonshot"); got != "kimi" {
		t.Fatalf("moonshot alias: got %q want %q", got, "kimi")
	}
	if got := CanonicalProviderKey("glm"); got != "glm" {
		t.Fatalf("unknown provider keys should pass through unchanged, got %q", got)
	}
}
```

```go
// internal/attractor/engine/config_test.go
func TestNormalizeProviderKey_DelegatesToProviderSpecAliases(t *testing.T) {
	if got := normalizeProviderKey("z-ai"); got != "zai" {
		t.Fatalf("normalizeProviderKey(z-ai)=%q want zai", got)
	}
	if got := normalizeProviderKey("moonshot"); got != "kimi" {
		t.Fatalf("normalizeProviderKey(moonshot)=%q want kimi", got)
	}
}
```

```go
// internal/llm/client_test.go
func TestNormalizeProviderName_DelegatesToProviderSpecAliases(t *testing.T) {
	if got := normalizeProviderName("gemini"); got != "google" {
		t.Fatalf("normalizeProviderName(gemini)=%q want google", got)
	}
	if got := normalizeProviderName("z-ai"); got != "zai" {
		t.Fatalf("normalizeProviderName(z-ai)=%q want zai", got)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/providerspec ./internal/attractor/engine ./internal/llm -run 'TestBuiltinSpecsIncludeCoreAndNewProviders|TestCanonicalProviderKey_Aliases|TestNormalizeProviderKey_DelegatesToProviderSpecAliases|TestNormalizeProviderName_DelegatesToProviderSpecAliases' -v`
Expected: FAIL (`internal/providerspec` package does not exist)

**Step 3: Write minimal implementation**

```go
package providerspec

import (
	"strings"
	"sync"
)

type APIProtocol string

const (
	ProtocolOpenAIResponses      APIProtocol = "openai_responses"
	ProtocolOpenAIChatCompletions APIProtocol = "openai_chat_completions"
	ProtocolAnthropicMessages    APIProtocol = "anthropic_messages"
	ProtocolGoogleGenerateContent APIProtocol = "google_generate_content"
)

type APISpec struct {
	Protocol           APIProtocol
	DefaultBaseURL     string
	DefaultPath        string
	DefaultAPIKeyEnv   string
	ProviderOptionsKey string
	ProfileFamily      string
}

type CLISpec struct {
	DefaultExecutable string
	InvocationTemplate []string
	PromptMode        string
	HelpProbeArgs     []string
	CapabilityAll     []string
	CapabilityAnyOf   [][]string
}

type Spec struct {
	Key      string
	Aliases  []string
	API      *APISpec
	CLI      *CLISpec
	Failover []string
}

var (
	providerAliasOnce  sync.Once
	providerAliasIndex map[string]string
)

func providerAliases() map[string]string {
	providerAliasOnce.Do(func() {
		providerAliasIndex = providerAliasIndexFromBuiltins(Builtins())
	})
	return providerAliasIndex
}

func providerAliasIndexFromBuiltins(specs map[string]Spec) map[string]string {
	out := map[string]string{}
	for key, spec := range specs {
		k := strings.ToLower(strings.TrimSpace(key))
		out[k] = k
		for _, alias := range spec.Aliases {
			a := strings.ToLower(strings.TrimSpace(alias))
			if a != "" {
				out[a] = k
			}
		}
	}
	return out
}

func CanonicalProviderKey(in string) string {
	k := strings.ToLower(strings.TrimSpace(in))
	if v, ok := providerAliases()[k]; ok {
		return v
	}
	return k
}

func CanonicalizeProviderList(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	seen := map[string]struct{}{}
	for _, raw := range in {
		k := CanonicalProviderKey(raw)
		if k == "" {
			continue
		}
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, k)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
```

```go
package providerspec

var builtinSpecs = map[string]Spec{
		"openai": {
			Key:     "openai",
			Aliases: nil,
			API: &APISpec{Protocol: ProtocolOpenAIResponses, DefaultBaseURL: "https://api.openai.com", DefaultPath: "/v1/responses", DefaultAPIKeyEnv: "OPENAI_API_KEY", ProviderOptionsKey: "openai", ProfileFamily: "openai"},
			CLI: &CLISpec{DefaultExecutable: "codex", InvocationTemplate: []string{"exec", "--json", "--sandbox", "workspace-write", "-m", "{{model}}", "-C", "{{worktree}}"}, PromptMode: "stdin", HelpProbeArgs: []string{"exec", "--help"}, CapabilityAll: []string{"--json", "--sandbox"}},
			Failover: []string{"anthropic", "google"},
		},
		"anthropic": {
			Key:     "anthropic",
			Aliases: nil,
			API: &APISpec{Protocol: ProtocolAnthropicMessages, DefaultBaseURL: "https://api.anthropic.com", DefaultPath: "/v1/messages", DefaultAPIKeyEnv: "ANTHROPIC_API_KEY", ProviderOptionsKey: "anthropic", ProfileFamily: "anthropic"},
			CLI: &CLISpec{DefaultExecutable: "claude", InvocationTemplate: []string{"-p", "--output-format", "stream-json", "--verbose", "--model", "{{model}}", "{{prompt}}"}, PromptMode: "arg", HelpProbeArgs: []string{"--help"}, CapabilityAll: []string{"--output-format", "stream-json", "--verbose"}},
			Failover: []string{"openai", "google"},
		},
		"google": {
			Key:     "google",
			Aliases: []string{"gemini"},
			API: &APISpec{Protocol: ProtocolGoogleGenerateContent, DefaultBaseURL: "https://generativelanguage.googleapis.com", DefaultPath: "/v1beta/models/{model}:generateContent", DefaultAPIKeyEnv: "GEMINI_API_KEY", ProviderOptionsKey: "google", ProfileFamily: "google"},
			CLI: &CLISpec{DefaultExecutable: "gemini", InvocationTemplate: []string{"-p", "--output-format", "stream-json", "--yolo", "--model", "{{model}}", "{{prompt}}"}, PromptMode: "arg", HelpProbeArgs: []string{"--help"}, CapabilityAll: []string{"--output-format"}, CapabilityAnyOf: [][]string{{"--yolo", "--approval-mode"}}},
			Failover: []string{"openai", "anthropic"},
		},
		"kimi": {
			Key:     "kimi",
			Aliases: []string{"moonshot"},
			API: &APISpec{Protocol: ProtocolOpenAIChatCompletions, DefaultBaseURL: "https://api.moonshot.ai", DefaultPath: "/v1/chat/completions", DefaultAPIKeyEnv: "KIMI_API_KEY", ProviderOptionsKey: "kimi", ProfileFamily: "openai"},
			Failover: []string{"openai", "zai"},
		},
		"zai": {
			Key:     "zai",
			Aliases: []string{"z-ai", "z.ai"},
			API: &APISpec{Protocol: ProtocolOpenAIChatCompletions, DefaultBaseURL: "https://api.z.ai", DefaultPath: "/api/paas/v4/chat/completions", DefaultAPIKeyEnv: "ZAI_API_KEY", ProviderOptionsKey: "zai", ProfileFamily: "openai"},
			Failover: []string{"openai", "kimi"},
		},
}

func Builtin(key string) (Spec, bool) {
	s, ok := builtinSpecs[CanonicalProviderKey(key)]
	if !ok {
		return Spec{}, false
	}
	return cloneSpec(s), true
}

func Builtins() map[string]Spec {
	out := make(map[string]Spec, len(builtinSpecs))
	for k, v := range builtinSpecs {
		out[k] = cloneSpec(v)
	}
	return out
}

func cloneSpec(in Spec) Spec {
	out := in
	if in.API != nil {
		api := *in.API
		out.API = &api
	}
	if in.CLI != nil {
		cli := *in.CLI
		cli.InvocationTemplate = append([]string{}, in.CLI.InvocationTemplate...)
		cli.HelpProbeArgs = append([]string{}, in.CLI.HelpProbeArgs...)
		cli.CapabilityAll = append([]string{}, in.CLI.CapabilityAll...)
		if len(in.CLI.CapabilityAnyOf) > 0 {
			cli.CapabilityAnyOf = make([][]string, 0, len(in.CLI.CapabilityAnyOf))
			for _, group := range in.CLI.CapabilityAnyOf {
				cli.CapabilityAnyOf = append(cli.CapabilityAnyOf, append([]string{}, group...))
			}
		}
		out.CLI = &cli
	}
	out.Aliases = append([]string{}, in.Aliases...)
	out.Failover = append([]string{}, in.Failover...)
	return out
}
```

```go
// internal/attractor/engine/config.go
func normalizeProviderKey(k string) string {
	return providerspec.CanonicalProviderKey(k)
}
```

```go
// internal/llm/client.go
func normalizeProviderName(name string) string {
	return providerspec.CanonicalProviderKey(name)
}
```

Alias-routing note:
- This normalization path is what allows request-level aliases (for example `req.Provider="z-ai"`) to resolve to adapters registered under canonical names (`zai`).

**Step 4: Run test to verify it passes**

Run: `go test ./internal/providerspec ./internal/attractor/engine ./internal/llm -run 'TestBuiltinSpecsIncludeCoreAndNewProviders|TestCanonicalProviderKey_Aliases|TestNormalizeProviderKey_DelegatesToProviderSpecAliases|TestNormalizeProviderName_DelegatesToProviderSpecAliases' -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/providerspec/spec.go internal/providerspec/builtin.go internal/providerspec/spec_test.go internal/attractor/engine/config.go internal/attractor/engine/config_test.go internal/llm/client.go internal/llm/client_test.go
git commit -m "feat(providerspec): add canonical provider registry and unify alias normalization across engine and llm client"
```

### Task 2: Extend Run Config Schema for Provider Plug-ins

**Files:**
- Modify: `internal/attractor/engine/config.go`
- Test: `internal/attractor/engine/config_test.go`

**Step 1: Write the failing test**

```go
func loadRunConfigFromBytesForTest(t *testing.T, yml []byte) (*RunConfigFile, error) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "run.yaml")
	if err := os.WriteFile(p, yml, 0o644); err != nil {
		t.Fatalf("write run.yaml: %v", err)
	}
	return LoadRunConfigFile(p)
}

func TestLoadRunConfig_CustomAPIProviderRequiresProtocol(t *testing.T) {
	yml := []byte(`
version: 1
repo: { path: /tmp/repo }
cxdb: { binary_addr: 127.0.0.1:9009, http_base_url: http://127.0.0.1:9010 }
modeldb: { litellm_catalog_path: /tmp/catalog.json }
llm:
  providers:
    acme:
      backend: api
`)
	_, err := loadRunConfigFromBytesForTest(t, yml)
	if err == nil || !strings.Contains(err.Error(), "llm.providers.acme.api.protocol") {
		t.Fatalf("expected protocol validation error, got %v", err)
	}
}

func TestLoadRunConfig_KimiAPIProtocolAccepted(t *testing.T) {
	yml := []byte(`
version: 1
repo: { path: /tmp/repo }
cxdb: { binary_addr: 127.0.0.1:9009, http_base_url: http://127.0.0.1:9010 }
modeldb: { litellm_catalog_path: /tmp/catalog.json }
llm:
  providers:
    kimi:
      backend: api
      api:
        protocol: openai_chat_completions
        api_key_env: KIMI_API_KEY
        base_url: https://api.moonshot.ai
        path: /v1/chat/completions
`)
	cfg, err := loadRunConfigFromBytesForTest(t, yml)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.LLM.Providers["kimi"].API.Protocol != "openai_chat_completions" {
		t.Fatalf("protocol not parsed")
	}
}

func TestLoadRunConfig_ZAIAliasAcceptedWithAPIProtocol(t *testing.T) {
	yml := []byte(`
version: 1
repo: { path: /tmp/repo }
cxdb: { binary_addr: 127.0.0.1:9009, http_base_url: http://127.0.0.1:9010 }
modeldb: { litellm_catalog_path: /tmp/catalog.json }
llm:
  providers:
    z-ai:
      backend: api
      api:
        protocol: openai_chat_completions
        api_key_env: ZAI_API_KEY
`)
	_, err := loadRunConfigFromBytesForTest(t, yml)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadRunConfig_BackwardCompatibleBuiltinProvidersStillValid(t *testing.T) {
	yml := []byte(`
version: 1
repo: { path: /tmp/repo }
cxdb: { binary_addr: 127.0.0.1:9009, http_base_url: http://127.0.0.1:9010 }
modeldb: { litellm_catalog_path: /tmp/catalog.json }
llm:
  providers:
    openai: { backend: api }
    anthropic: { backend: api }
    google: { backend: api }
`)
	_, err := loadRunConfigFromBytesForTest(t, yml)
	if err != nil {
		t.Fatalf("unexpected backward-compat validation error: %v", err)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/attractor/engine -run 'TestLoadRunConfig_CustomAPIProviderRequiresProtocol|TestLoadRunConfig_KimiAPIProtocolAccepted|TestLoadRunConfig_ZAIAliasAcceptedWithAPIProtocol|TestLoadRunConfig_BackwardCompatibleBuiltinProvidersStillValid' -v`
Expected: FAIL (new `api` fields missing from schema/validation)

**Step 3: Write minimal implementation**

```go
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
	Backend    BackendKind        `json:"backend" yaml:"backend"`
	Executable string             `json:"executable,omitempty" yaml:"executable,omitempty"`
	API        ProviderAPIConfig  `json:"api,omitempty" yaml:"api,omitempty"`
	Failover   []string           `json:"failover,omitempty" yaml:"failover,omitempty"`
}
```

```go
// Remove the old hard-coded provider allowlist switch entirely:
//   switch normalizeProviderKey(prov) { case "openai","anthropic","google": ... }
// and replace it with protocol-driven validation:
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
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/attractor/engine -run 'TestLoadRunConfig_CustomAPIProviderRequiresProtocol|TestLoadRunConfig_KimiAPIProtocolAccepted|TestLoadRunConfig_ZAIAliasAcceptedWithAPIProtocol|TestLoadRunConfig_BackwardCompatibleBuiltinProvidersStillValid' -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/attractor/engine/config.go internal/attractor/engine/config_test.go
git commit -m "feat(config): add provider api schema fields and validation for protocol-driven providers"
```

### Task 3: Build Runtime Provider Definitions (Merged Defaults + Overrides)

**Files:**
- Create: `internal/attractor/engine/provider_runtime.go`
- Test: `internal/attractor/engine/provider_runtime_test.go`

**Step 1: Write the failing test**

```go
func TestResolveProviderRuntimes_MergesBuiltinAndConfigOverrides(t *testing.T) {
	cfg := &RunConfigFile{}
	cfg.LLM.Providers = map[string]ProviderConfig{
		"kimi": {Backend: BackendAPI, API: ProviderAPIConfig{Protocol: "openai_chat_completions", APIKeyEnv: "KIMI_API_KEY", Headers: map[string]string{"X-Trace": "1"}}},
	}
	rt, err := resolveProviderRuntimes(cfg)
	if err != nil {
		t.Fatalf("resolveProviderRuntimes: %v", err)
	}
	if rt["kimi"].API.Protocol != "openai_chat_completions" {
		t.Fatalf("kimi protocol mismatch")
	}
	if _, ok := rt["openai"]; !ok {
		t.Fatalf("expected failover target runtime for openai")
	}
	if rt["openai"].API.DefaultPath != "/v1/responses" {
		t.Fatalf("expected synthesized openai default path")
	}
	if got := rt["kimi"].APIHeaders(); got["X-Trace"] != "1" {
		t.Fatalf("expected runtime headers copy, got %v", got)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/attractor/engine -run TestResolveProviderRuntimes_MergesBuiltinAndConfigOverrides -v`
Expected: FAIL (`resolveProviderRuntimes` undefined)

**Step 3: Write minimal implementation**

```go
type ProviderRuntime struct {
	Key           string
	Backend       BackendKind
	Executable    string
	API           providerspec.APISpec
	CLI           *providerspec.CLISpec
	APIHeadersMap map[string]string
	Failover      []string
	ProfileFamily string
}

func (r ProviderRuntime) APIHeaders() map[string]string {
	return cloneStringMap(r.APIHeadersMap)
}

func resolveProviderRuntimes(cfg *RunConfigFile) (map[string]ProviderRuntime, error) {
	out := map[string]ProviderRuntime{}
	for rawKey, pc := range cfg.LLM.Providers {
		key := providerspec.CanonicalProviderKey(rawKey)
		b, _ := providerspec.Builtin(key)
		rt := ProviderRuntime{
			Key:        key,
			Backend:    pc.Backend,
			Executable: strings.TrimSpace(pc.Executable),
			CLI:        cloneCLISpec(b.CLI),
		}
		if b.API != nil {
			rt.API = *b.API
		}
		if p := strings.TrimSpace(pc.API.Protocol); p != "" {
			rt.API.Protocol = providerspec.APIProtocol(p)
		}
		if v := strings.TrimSpace(pc.API.BaseURL); v != "" {
			rt.API.DefaultBaseURL = v
		}
		if v := strings.TrimSpace(pc.API.Path); v != "" {
			rt.API.DefaultPath = v
		}
		if v := strings.TrimSpace(pc.API.APIKeyEnv); v != "" {
			rt.API.DefaultAPIKeyEnv = v
		}
		if v := strings.TrimSpace(pc.API.ProviderOptionsKey); v != "" {
			rt.API.ProviderOptionsKey = v
		}
		if v := strings.TrimSpace(pc.API.ProfileFamily); v != "" {
			rt.API.ProfileFamily = v
		}
		rt.APIHeadersMap = cloneStringMap(pc.API.Headers)
		rt.ProfileFamily = rt.API.ProfileFamily
		if len(pc.Failover) > 0 {
			rt.Failover = providerspec.CanonicalizeProviderList(pc.Failover)
		} else if len(b.Failover) > 0 {
			rt.Failover = providerspec.CanonicalizeProviderList(b.Failover)
		}
		out[key] = rt
	}

	// Ensure failover targets are resolvable even when not explicitly configured.
	// Iterate to closure so nested failover chains are also synthesized.
	queue := make([]string, 0, len(out))
	for k := range out {
		queue = append(queue, k)
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		rt := out[cur]
		for _, target := range rt.Failover {
			if _, ok := out[target]; ok {
				continue
			}
			b, ok := providerspec.Builtin(target)
			if !ok {
				continue
			}
			synth := ProviderRuntime{
				Key:        target,
				Backend:    defaultBackendForSpec(b),
				Executable: "",
				CLI:        cloneCLISpec(b.CLI),
				Failover:   providerspec.CanonicalizeProviderList(b.Failover),
			}
			if b.API != nil {
				synth.API = *b.API
				synth.ProfileFamily = b.API.ProfileFamily
			}
			out[target] = synth
			queue = append(queue, target)
		}
	}
	return out, nil
}

func defaultBackendForSpec(spec providerspec.Spec) BackendKind {
	if spec.API != nil {
		return BackendAPI
	}
	return BackendCLI
}

func cloneCLISpec(in *providerspec.CLISpec) *providerspec.CLISpec {
	if in == nil {
		return nil
	}
	cp := *in
	cp.InvocationTemplate = append([]string{}, in.InvocationTemplate...)
	cp.HelpProbeArgs = append([]string{}, in.HelpProbeArgs...)
	cp.CapabilityAll = append([]string{}, in.CapabilityAll...)
	if len(in.CapabilityAnyOf) > 0 {
		cp.CapabilityAnyOf = make([][]string, 0, len(in.CapabilityAnyOf))
		for _, group := range in.CapabilityAnyOf {
			cp.CapabilityAnyOf = append(cp.CapabilityAnyOf, append([]string{}, group...))
		}
	}
	return &cp
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/attractor/engine -run TestResolveProviderRuntimes_MergesBuiltinAndConfigOverrides -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/attractor/engine/provider_runtime.go internal/attractor/engine/provider_runtime_test.go
git commit -m "feat(engine): resolve runtime provider definitions from builtin specs and config overrides"
```

### Task 4: Refactor API Client Construction to Protocol Factories

**Files:**
- Create: `internal/attractor/engine/api_client_from_runtime.go`
- Create: `internal/attractor/engine/api_client_from_runtime_test.go`
- Modify: `internal/llm/providers/openai/adapter.go`
- Modify: `internal/llm/providers/anthropic/adapter.go`
- Modify: `internal/llm/providers/google/adapter.go`
- Test: `internal/llm/providers/openai/adapter_test.go`
- Test: `internal/llm/providers/anthropic/adapter_test.go`
- Test: `internal/llm/providers/google/adapter_test.go`

**Step 1: Write the failing test**

```go
func TestNewAPIClientFromProviderRuntimes_RegistersAdaptersByProtocol(t *testing.T) {
	runtimes := map[string]ProviderRuntime{
		"openai": {Key: "openai", Backend: BackendAPI, API: providerspec.APISpec{Protocol: providerspec.ProtocolOpenAIResponses, DefaultBaseURL: "http://127.0.0.1:0", DefaultAPIKeyEnv: "OPENAI_API_KEY", ProviderOptionsKey: "openai"}},
	}
	t.Setenv("OPENAI_API_KEY", "test-key")
	c, err := newAPIClientFromProviderRuntimes(runtimes)
	if err != nil {
		t.Fatalf("newAPIClientFromProviderRuntimes: %v", err)
	}
	if len(c.ProviderNames()) != 1 {
		t.Fatalf("expected one adapter")
	}
}

func TestNewAPIClientFromProviderRuntimes_CLIOnlyIsNotAnError(t *testing.T) {
	runtimes := map[string]ProviderRuntime{
		"openai": {Key: "openai", Backend: BackendCLI},
	}
	c, err := newAPIClientFromProviderRuntimes(runtimes)
	if err != nil {
		t.Fatalf("expected nil error for cli-only runtimes, got %v", err)
	}
	if len(c.ProviderNames()) != 0 {
		t.Fatalf("expected zero adapters, got %v", c.ProviderNames())
	}
}

func TestOpenAIAdapter_NewWithProvider_UsesConfiguredName(t *testing.T) {
	a := openai.NewWithProvider("kimi", "k", "https://api.example.com")
	if got := a.Name(); got != "kimi" {
		t.Fatalf("Name()=%q want kimi", got)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/attractor/engine ./internal/llm/providers/openai ./internal/llm/providers/anthropic ./internal/llm/providers/google -run 'TestNewAPIClientFromProviderRuntimes_RegistersAdaptersByProtocol|TestNewAPIClientFromProviderRuntimes_CLIOnlyIsNotAnError|TestOpenAIAdapter_NewWithProvider_UsesConfiguredName' -v`
Expected: FAIL (`newAPIClientFromProviderRuntimes` undefined)

**Step 3: Write minimal implementation**

```go
func newAPIClientFromProviderRuntimes(runtimes map[string]ProviderRuntime) (*llm.Client, error) {
	c := llm.NewClient()
	keys := sortedKeys(runtimes)
	for _, key := range keys {
		rt := runtimes[key]
		if rt.Backend != BackendAPI {
			continue
		}
		apiKey := strings.TrimSpace(os.Getenv(rt.API.DefaultAPIKeyEnv))
		if apiKey == "" {
			continue
		}
		switch rt.API.Protocol {
		case providerspec.ProtocolOpenAIResponses:
			c.Register(openai.NewWithProvider(key, apiKey, rt.API.DefaultBaseURL))
		case providerspec.ProtocolAnthropicMessages:
			c.Register(anthropic.NewWithProvider(key, apiKey, rt.API.DefaultBaseURL))
		case providerspec.ProtocolGoogleGenerateContent:
			c.Register(google.NewWithProvider(key, apiKey, rt.API.DefaultBaseURL))
		case providerspec.ProtocolOpenAIChatCompletions:
			// Task 5 adds openaicompat registration. Keep Task 4 bisect-safe by
			// skipping this protocol rather than returning a hard error.
			continue
		default:
			return nil, fmt.Errorf("unsupported api protocol %q for provider %s", rt.API.Protocol, key)
		}
	}
	// Empty client is valid here (for example CLI-only runs). Callers decide
	// whether an API backend is actually required for the current node.
	return c, nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
```

```go
// openai/adapter.go (same constructor pattern for anthropic/google)
type Adapter struct {
	Provider string
	APIKey   string
	BaseURL  string
	Client   *http.Client
}

func NewWithProvider(provider, apiKey, baseURL string) *Adapter {
	p := providerspec.CanonicalProviderKey(provider)
	if p == "" {
		p = "openai"
	}
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		base = "https://api.openai.com"
	}
	return &Adapter{
		Provider: p,
		APIKey:   strings.TrimSpace(apiKey),
		BaseURL:  base,
		Client:   &http.Client{Timeout: 0},
	}
}

func NewFromEnv() (*Adapter, error) {
	key := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if key == "" {
		return nil, fmt.Errorf("OPENAI_API_KEY is required")
	}
	return NewWithProvider("openai", key, os.Getenv("OPENAI_BASE_URL")), nil
}

func (a *Adapter) Name() string {
	if p := providerspec.CanonicalProviderKey(a.Provider); p != "" {
		return p
	}
	return "openai"
}
```

Apply the same constructor/name pattern in:
- `internal/llm/providers/anthropic/adapter.go` using default base URL `https://api.anthropic.com`
- `internal/llm/providers/google/adapter.go` using default base URL `https://generativelanguage.googleapis.com`

```go
// anthropic/adapter.go (same structure for google with provider="google")
func NewFromEnv() (*Adapter, error) {
	key := strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY"))
	if key == "" {
		return nil, fmt.Errorf("ANTHROPIC_API_KEY is required")
	}
	return NewWithProvider("anthropic", key, os.Getenv("ANTHROPIC_BASE_URL")), nil
}
```

```go
// google/adapter.go
func NewFromEnv() (*Adapter, error) {
	key := strings.TrimSpace(os.Getenv("GEMINI_API_KEY"))
	if key == "" {
		key = strings.TrimSpace(os.Getenv("GOOGLE_API_KEY"))
	}
	if key == "" {
		return nil, fmt.Errorf("GEMINI_API_KEY is required")
	}
	return NewWithProvider("google", key, os.Getenv("GEMINI_BASE_URL")), nil
}
```

Backward-compatibility rule:
- Keep each provider `init()` env registration factory as-is.
- `init()` continues to call `NewFromEnv()`, and `NewFromEnv()` must always set the canonical provider key (`openai`/`anthropic`/`google`) so adapter registration names remain stable.
- `Name()` must provide legacy defaults when `Provider` is empty (`openai`/`anthropic`/`google`) so existing struct literals in tests keep working.
- Anthropic example: `func (a *Adapter) Name() string { if p := providerspec.CanonicalProviderKey(a.Provider); p != "" { return p }; return "anthropic" }`
- Google example: `func (a *Adapter) Name() string { if p := providerspec.CanonicalProviderKey(a.Provider); p != "" { return p }; return "google" }`
- Audit direct adapter literals after struct change with `rg -n "openai\\.Adapter\\{|anthropic\\.Adapter\\{|google\\.Adapter\\{" internal -g '*_test.go'`.
- Convert remaining literals to constructor usage or set `Provider` explicitly where needed in the same commit as the struct field addition (never as a follow-up commit).

Sequencing note:
- Task 4 intentionally wires OpenAI/Anthropic/Google first and treats `openai_chat_completions` as a no-op placeholder (not a runtime error) to keep intermediate commits runnable.
- Task 5 adds `ProtocolOpenAIChatCompletions` registration and updates `internal/attractor/engine/api_client_from_runtime.go` in the same commit as `openaicompat`.

**Step 4: Run test to verify it passes**

Run: `go test ./internal/attractor/engine ./internal/llm/providers/openai ./internal/llm/providers/anthropic ./internal/llm/providers/google -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/attractor/engine/api_client_from_runtime.go internal/attractor/engine/api_client_from_runtime_test.go internal/llm/providers/openai/adapter.go internal/llm/providers/openai/adapter_test.go internal/llm/providers/anthropic/adapter.go internal/llm/providers/anthropic/adapter_test.go internal/llm/providers/google/adapter.go internal/llm/providers/google/adapter_test.go
git commit -m "refactor(engine): construct API adapters from runtime provider protocol metadata"
```

### Task 5: Implement Generic OpenAI Chat Completions Adapter

**Files:**
- Create: `internal/llm/providers/openaicompat/adapter.go`
- Test: `internal/llm/providers/openaicompat/adapter_test.go`
- Modify: `internal/attractor/engine/api_client_from_runtime.go`
- Test: `internal/attractor/engine/api_client_from_runtime_test.go`

**Step 1: Write the failing test**

```go
func TestAdapter_Complete_ChatCompletionsMapsToolCalls(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"id":"c1","model":"m","choices":[{"finish_reason":"tool_calls","message":{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"read_file","arguments":"{\"file_path\":\"README.md\"}"}}]}}],"usage":{"prompt_tokens":10,"completion_tokens":3,"total_tokens":13}}`))
	}))
	defer srv.Close()

	a := NewAdapter(Config{Provider: "kimi", APIKey: "k", BaseURL: srv.URL, Path: "/v1/chat/completions", OptionsKey: "kimi"})
	resp, err := a.Complete(context.Background(), llm.Request{Provider: "kimi", Model: "kimi-k2.5", Messages: []llm.Message{llm.User("hi")}})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(resp.ToolCalls()) != 1 {
		t.Fatalf("tool call mapping failed")
	}
}

func TestAdapter_Stream_EmitsFinishEvent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"id\":\"c2\",\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":null}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"id\":\"c2\",\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	a := NewAdapter(Config{Provider: "zai", APIKey: "k", BaseURL: srv.URL})
	stream, err := a.Stream(context.Background(), llm.Request{Provider: "zai", Model: "glm-4.7", Messages: []llm.Message{llm.User("hi")}})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer stream.Close()
	sawFinish := false
	for ev := range stream.Events() {
		if ev.Type == llm.StreamEventFinish {
			sawFinish = true
			break
		}
	}
	if !sawFinish {
		t.Fatalf("expected finish event")
	}
}

func TestAdapter_Stream_RequestBodyPreservesLargeIntegerOptions(t *testing.T) {
	const big = "9007199254740993"
	var seen map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		dec := json.NewDecoder(r.Body)
		dec.UseNumber()
		if err := dec.Decode(&seen); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()
	a := NewAdapter(Config{Provider: "kimi", APIKey: "k", BaseURL: srv.URL, OptionsKey: "kimi"})
	stream, err := a.Stream(context.Background(), llm.Request{
		Provider: "kimi",
		Model:    "kimi-k2.5",
		Messages: []llm.Message{llm.User("hi")},
		ProviderOptions: map[string]any{
			"kimi": map[string]any{"seed": json.Number(big)},
		},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	stream.Close()
	if got, ok := seen["seed"].(json.Number); !ok || got.String() != big {
		t.Fatalf("seed mismatch: %#v", seen["seed"])
	}
}

func TestAdapter_Stream_ParsesMultiLineSSEData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hel\"}}]}\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"lo\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()
	a := NewAdapter(Config{Provider: "zai", APIKey: "k", BaseURL: srv.URL})
	stream, err := a.Stream(context.Background(), llm.Request{Provider: "zai", Model: "glm-4.7", Messages: []llm.Message{llm.User("hi")}})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer stream.Close()
	var text strings.Builder
	for ev := range stream.Events() {
		if ev.Type == llm.StreamEventTextDelta {
			text.WriteString(ev.Delta)
		}
	}
	if text.String() != "hello" {
		t.Fatalf("text delta mismatch: %q", text.String())
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/llm/providers/openaicompat -run 'TestAdapter_Complete_ChatCompletionsMapsToolCalls|TestAdapter_Stream_EmitsFinishEvent|TestAdapter_Stream_RequestBodyPreservesLargeIntegerOptions|TestAdapter_Stream_ParsesMultiLineSSEData' -v`
Expected: FAIL (package/adapter missing)

**Step 3: Write minimal implementation**

```go
type Config struct {
	Provider     string
	APIKey       string
	BaseURL      string
	Path         string
	OptionsKey   string
	ExtraHeaders map[string]string
}

type Adapter struct {
	cfg    Config
	client *http.Client
}

func NewAdapter(cfg Config) *Adapter {
	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if strings.TrimSpace(cfg.Path) == "" {
		cfg.Path = "/v1/chat/completions"
	}
	if strings.TrimSpace(cfg.OptionsKey) == "" {
		cfg.OptionsKey = strings.TrimSpace(cfg.Provider)
	}
	if cfg.Provider == "" {
		cfg.Provider = cfg.OptionsKey
	}
	// Streaming adapters should rely on request context deadlines/cancellation.
	return &Adapter{cfg: cfg, client: &http.Client{Timeout: 0}}
}

func (a *Adapter) Name() string { return a.cfg.Provider }

func (a *Adapter) Complete(ctx context.Context, req llm.Request) (llm.Response, error) {
	body, err := toChatCompletionsBody(req, a.cfg.OptionsKey, chatCompletionsBodyOptions{})
	if err != nil {
		return llm.Response{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.cfg.BaseURL+a.cfg.Path, bytes.NewReader(body))
	if err != nil {
		return llm.Response{}, llm.WrapContextError(a.cfg.Provider, err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+a.cfg.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")
	for k, v := range a.cfg.ExtraHeaders {
		httpReq.Header.Set(k, v)
	}
	resp, err := a.client.Do(httpReq)
	if err != nil {
		return llm.Response{}, llm.WrapContextError(a.cfg.Provider, err)
	}
	defer resp.Body.Close()
	return parseChatCompletionsResponse(a.cfg.Provider, req.Model, resp)
}

func (a *Adapter) Stream(ctx context.Context, req llm.Request) (llm.Stream, error) {
	sctx, cancel := context.WithCancel(ctx)
	body, err := toChatCompletionsBody(req, a.cfg.OptionsKey, chatCompletionsBodyOptions{
		Stream:       true,
		IncludeUsage: true,
	})
	if err != nil {
		cancel()
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(sctx, http.MethodPost, a.cfg.BaseURL+a.cfg.Path, bytes.NewReader(body))
	if err != nil {
		cancel()
		return nil, llm.WrapContextError(a.cfg.Provider, err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+a.cfg.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")
	for k, v := range a.cfg.ExtraHeaders {
		httpReq.Header.Set(k, v)
	}

	resp, err := a.client.Do(httpReq)
	if err != nil {
		cancel()
		return nil, llm.WrapContextError(a.cfg.Provider, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		cancel()
		_, perr := parseChatCompletionsResponse(a.cfg.Provider, req.Model, resp)
		return nil, perr
	}

	s := llm.NewChanStream(cancel)
	go func() {
		defer resp.Body.Close()
		defer s.CloseSend()
		s.Send(llm.StreamEvent{Type: llm.StreamEventStreamStart})
		state := &chatStreamState{Provider: a.cfg.Provider, Model: req.Model, TextID: "assistant_text"}
		err := llm.ParseSSE(sctx, resp.Body, func(ev llm.SSEEvent) error {
			payload := strings.TrimSpace(string(ev.Data))
			if payload == "" {
				return nil
			}
			if payload == "[DONE]" {
				final := state.FinalResponse()
				s.Send(llm.StreamEvent{Type: llm.StreamEventFinish, FinishReason: &final.Finish, Usage: &final.Usage, Response: &final})
				return nil
			}
			var chunk map[string]any
			dec := json.NewDecoder(strings.NewReader(payload))
			dec.UseNumber()
			if err := dec.Decode(&chunk); err != nil {
				return err
			}
			emitChatCompletionsChunkEvents(s, state, chunk)
			return nil
		})
		if err != nil && !errors.Is(err, context.Canceled) {
			s.Send(llm.StreamEvent{Type: llm.StreamEventError, Err: llm.NewStreamError(a.cfg.Provider, err.Error())})
		}
	}()
	return s, nil
}

type chatCompletionsBodyOptions struct {
	Stream       bool
	IncludeUsage bool
}

func toChatCompletionsBody(req llm.Request, optionsKey string, opts chatCompletionsBodyOptions) ([]byte, error) {
	body := map[string]any{
		"model":    req.Model,
		"messages": toChatCompletionsMessages(req.Messages),
	}
	if len(req.Tools) > 0 {
		body["tools"] = toChatCompletionsTools(req.Tools)
	}
	if req.ToolChoice != nil {
		body["tool_choice"] = toChatCompletionsToolChoice(*req.ToolChoice)
	}
	if req.ProviderOptions != nil {
		if ov, ok := req.ProviderOptions[optionsKey].(map[string]any); ok {
			for k, v := range ov {
				body[k] = v
			}
		}
	}
	if opts.Stream {
		body["stream"] = true
		if opts.IncludeUsage {
			body["stream_options"] = map[string]any{"include_usage": true}
		}
	}
	return json.Marshal(body)
}

func parseChatCompletionsResponse(provider, model string, resp *http.Response) (llm.Response, error) {
	rawBytes, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return llm.Response{}, llm.WrapContextError(provider, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw := map[string]any{}
		dec := json.NewDecoder(bytes.NewReader(rawBytes))
		dec.UseNumber()
		if err := dec.Decode(&raw); err != nil {
			raw["raw_body"] = string(rawBytes)
		}
		ra := llm.ParseRetryAfter(resp.Header.Get("Retry-After"), time.Now())
		return llm.Response{}, llm.ErrorFromHTTPStatus(provider, resp.StatusCode, "chat.completions failed", raw, ra)
	}
	var raw map[string]any
	dec := json.NewDecoder(bytes.NewReader(rawBytes))
	dec.UseNumber()
	if err := dec.Decode(&raw); err != nil {
		return llm.Response{}, llm.WrapContextError(provider, err)
	}
	return fromChatCompletions(provider, model, raw)
}

func toChatCompletionsMessages(msgs []llm.Message) []map[string]any {
	out := make([]map[string]any, 0, len(msgs))
	for _, m := range msgs {
		entry := map[string]any{"role": string(m.Role)}
		textParts := []string{}
		toolCalls := []map[string]any{}
		for _, p := range m.Content {
			switch p.Kind {
			case llm.ContentText:
				if strings.TrimSpace(p.Text) != "" {
					textParts = append(textParts, p.Text)
				}
			case llm.ContentToolCall:
				if p.ToolCall != nil {
					toolCalls = append(toolCalls, map[string]any{
						"id":   p.ToolCall.ID,
						"type": "function",
						"function": map[string]any{
							"name":      p.ToolCall.Name,
							"arguments": string(p.ToolCall.Arguments),
						},
					})
				}
			case llm.ContentToolResult:
				if p.ToolResult != nil {
					entry["role"] = "tool"
					entry["tool_call_id"] = p.ToolResult.ToolCallID
					entry["content"] = renderAnyAsText(p.ToolResult.Content)
				}
			}
		}
		if _, ok := entry["content"]; !ok {
			entry["content"] = strings.Join(textParts, "\n")
		}
		if len(toolCalls) > 0 {
			entry["tool_calls"] = toolCalls
		}
		out = append(out, entry)
	}
	return out
}

func toChatCompletionsTools(tools []llm.ToolDefinition) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, td := range tools {
		out = append(out, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        td.Name,
				"description": td.Description,
				"parameters":  td.Parameters,
			},
		})
	}
	return out
}

func toChatCompletionsToolChoice(tc llm.ToolChoice) any {
	mode := strings.ToLower(strings.TrimSpace(tc.Mode))
	switch mode {
	case "", "auto":
		return "auto"
	case "none":
		return "none"
	case "required":
		return "required"
	case "named":
		return map[string]any{"type": "function", "function": map[string]any{"name": tc.Name}}
	default:
		return "auto"
	}
}

func fromChatCompletions(provider, model string, raw map[string]any) (llm.Response, error) {
	choicesAny, ok := raw["choices"].([]any)
	if !ok || len(choicesAny) == 0 {
		return llm.Response{}, fmt.Errorf("chat.completions response missing choices")
	}
	choice, ok := choicesAny[0].(map[string]any)
	if !ok {
		return llm.Response{}, fmt.Errorf("chat.completions first choice malformed")
	}
	msgMap, _ := choice["message"].(map[string]any)
	msg := llm.Assistant(asString(msgMap["content"]))
	if callsAny, ok := msgMap["tool_calls"].([]any); ok {
		for _, c := range callsAny {
			cm, _ := c.(map[string]any)
			fn, _ := cm["function"].(map[string]any)
			msg.Content = append(msg.Content, llm.ContentPart{
				Kind: llm.ContentToolCall,
				ToolCall: &llm.ToolCallData{
					ID:        asString(cm["id"]),
					Type:      asString(cm["type"]),
					Name:      asString(fn["name"]),
					Arguments: json.RawMessage(renderAnyAsText(fn["arguments"])),
				},
			})
		}
	}
	usageMap, _ := raw["usage"].(map[string]any)
	return llm.Response{
		ID:       asString(raw["id"]),
		Model:    firstNonEmpty(model, asString(raw["model"])),
		Provider: provider,
		Message:  msg,
		Finish:   llm.FinishReason{Reason: normalizeFinishReason(asString(choice["finish_reason"])), Raw: asString(choice["finish_reason"])},
		Usage: llm.Usage{
			InputTokens:  intFromAny(usageMap["prompt_tokens"]),
			OutputTokens: intFromAny(usageMap["completion_tokens"]),
			TotalTokens:  intFromAny(usageMap["total_tokens"]),
		},
		Raw: raw,
	}, nil
}

func renderAnyAsText(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}

func asString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case json.Number:
		return x.String()
	default:
		return ""
	}
}

func intFromAny(v any) int {
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	case json.Number:
		i, _ := x.Int64()
		return int(i)
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(x))
		return n
	default:
		return 0
	}
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return strings.TrimSpace(a)
	}
	return strings.TrimSpace(b)
}

func normalizeFinishReason(in string) string {
	switch strings.ToLower(strings.TrimSpace(in)) {
	case "tool_calls":
		return "tool_call"
	case "length":
		return "max_tokens"
	default:
		return strings.ToLower(strings.TrimSpace(in))
	}
}

type chatStreamState struct {
	Provider   string
	Model      string
	TextID     string
	Text       strings.Builder
	TextOpen   bool
	Finish     llm.FinishReason
	Usage      llm.Usage
	UsageValid bool
}

func (st *chatStreamState) FinalResponse() llm.Response {
	msg := llm.Assistant(st.Text.String())
	finish := st.Finish
	if strings.TrimSpace(finish.Reason) == "" {
		finish = llm.FinishReason{Reason: "stop", Raw: "stop"}
	}
	return llm.Response{
		Provider: st.Provider,
		Model:    st.Model,
		Message:  msg,
		Finish:   finish,
		Usage:    st.Usage,
	}
}

func emitChatCompletionsChunkEvents(s *llm.ChanStream, st *chatStreamState, chunk map[string]any) {
	choices, _ := chunk["choices"].([]any)
	if len(choices) == 0 {
		return
	}
	choice, _ := choices[0].(map[string]any)
	delta, _ := choice["delta"].(map[string]any)

	if text, ok := delta["content"].(string); ok && text != "" {
		if !st.TextOpen {
			st.TextOpen = true
			s.Send(llm.StreamEvent{Type: llm.StreamEventTextStart, TextID: st.TextID})
		}
		st.Text.WriteString(text)
		s.Send(llm.StreamEvent{Type: llm.StreamEventTextDelta, TextID: st.TextID, Delta: text})
	}

	if fin := strings.TrimSpace(asString(choice["finish_reason"])); fin != "" {
		st.Finish = llm.FinishReason{Reason: normalizeFinishReason(fin), Raw: fin}
		if st.TextOpen {
			s.Send(llm.StreamEvent{Type: llm.StreamEventTextEnd, TextID: st.TextID})
			st.TextOpen = false
		}
		s.Send(llm.StreamEvent{Type: llm.StreamEventStepFinish, FinishReason: &st.Finish})
	}

	if usageMap, ok := chunk["usage"].(map[string]any); ok {
		st.Usage = llm.Usage{
			InputTokens:  intFromAny(usageMap["prompt_tokens"]),
			OutputTokens: intFromAny(usageMap["completion_tokens"]),
			TotalTokens:  intFromAny(usageMap["total_tokens"]),
		}
		st.UsageValid = true
	}
}
```

```go
// internal/attractor/engine/api_client_from_runtime.go (Task 5 follow-up wiring)
case providerspec.ProtocolOpenAIChatCompletions:
	c.Register(openaicompat.NewAdapter(openaicompat.Config{
		Provider:     key,
		APIKey:       apiKey,
		BaseURL:      rt.API.DefaultBaseURL,
		Path:         rt.API.DefaultPath,
		OptionsKey:   rt.API.ProviderOptionsKey,
		ExtraHeaders: rt.APIHeaders(),
	}))
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/llm/providers/openaicompat ./internal/attractor/engine -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/llm/providers/openaicompat/adapter.go internal/llm/providers/openaicompat/adapter_test.go internal/attractor/engine/api_client_from_runtime.go internal/attractor/engine/api_client_from_runtime_test.go
git commit -m "feat(llm): add generic OpenAI Chat Completions adapter for protocol-based providers"
```

### Task 6: Refactor API Routing, Agent Profile Selection, and Failover to Runtime Metadata

**Files:**
- Modify: `internal/attractor/engine/codergen_router.go`
- Modify: `internal/attractor/engine/run_with_config.go`
- Create: `internal/agent/profile_registry.go`
- Test: `internal/agent/profile_test.go`
- Test: `internal/attractor/engine/codergen_failover_test.go`

**Step 1: Write the failing test**

```go
func TestProfileForRuntimeProvider_UsesConfiguredProfileFamily(t *testing.T) {
	rt := ProviderRuntime{Key: "zai", ProfileFamily: "openai"}
	p, err := profileForRuntimeProvider(rt, "glm-4.7")
	if err != nil {
		t.Fatalf("profileForRuntimeProvider: %v", err)
	}
	if p.ID() != "openai" {
		t.Fatalf("expected openai family profile")
	}
}

func TestFailoverOrder_UsesRuntimeProviderPolicy(t *testing.T) {
	rt := map[string]ProviderRuntime{
		"kimi": {Key: "kimi", Failover: []string{"zai", "openai"}},
	}
	got := failoverOrderFromRuntime("kimi", rt)
	if strings.Join(got, ",") != "zai,openai" {
		t.Fatalf("failover mismatch: %v", got)
	}
}

func TestPickFailoverModelFromRuntime_NeverReturnsEmptyForConfiguredProvider(t *testing.T) {
	rt := map[string]ProviderRuntime{
		"zai": {Key: "zai"},
	}
	got := pickFailoverModelFromRuntime("zai", rt, nil, "glm-4.7")
	if got != "glm-4.7" {
		t.Fatalf("expected fallback model, got %q", got)
	}
}

func TestEnsureAPIClient_UsesSyncOnce(t *testing.T) {
	var calls atomic.Int32
	r := NewCodergenRouterWithRuntimes(&RunConfigFile{}, nil, map[string]ProviderRuntime{
		"openai": {Key: "openai", Backend: BackendAPI, API: providerspec.APISpec{Protocol: providerspec.ProtocolOpenAIResponses}},
	})
	r.apiClientFactory = func(map[string]ProviderRuntime) (*llm.Client, error) {
		calls.Add(1)
		return llm.NewClient(), nil
	}

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = r.ensureAPIClient()
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("api client factory called %d times; want 1", calls.Load())
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/agent ./internal/attractor/engine -run 'TestProfileForRuntimeProvider_UsesConfiguredProfileFamily|TestFailoverOrder_UsesRuntimeProviderPolicy|TestPickFailoverModelFromRuntime_NeverReturnsEmptyForConfiguredProvider|TestEnsureAPIClient_UsesSyncOnce' -v`
Expected: FAIL (`profileForRuntimeProvider` / `failoverOrderFromRuntime` / `pickFailoverModelFromRuntime` / `ensureAPIClient` missing)

**Step 3: Write minimal implementation**

```go
// internal/agent/profile_registry.go
var (
	profileFactoriesMu sync.RWMutex
	profileFactories   = map[string]func(string) ProviderProfile{
		"openai":    NewOpenAIProfile,
		"anthropic": NewAnthropicProfile,
		"google":    NewGeminiProfile,
	}
)

func RegisterProfileFamily(family string, factory func(string) ProviderProfile) {
	k := strings.ToLower(strings.TrimSpace(family))
	if k == "" || factory == nil {
		return
	}
	profileFactoriesMu.Lock()
	profileFactories[k] = factory
	profileFactoriesMu.Unlock()
}

func NewProfileForFamily(family string, model string) (ProviderProfile, error) {
	f := strings.ToLower(strings.TrimSpace(family))
	profileFactoriesMu.RLock()
	factory, ok := profileFactories[f]
	profileFactoriesMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unsupported profile family: %s", family)
	}
	return factory(model), nil
}
```

```go
// codergen_router.go (struct + constructor migration)
type CodergenRouter struct {
	cfg     *RunConfigFile
	catalog *modeldb.LiteLLMCatalog

	providerRuntimes map[string]ProviderRuntime
	apiClientFactory func(map[string]ProviderRuntime) (*llm.Client, error) // test seam

	apiOnce   sync.Once
	apiClient *llm.Client
	apiErr    error
}

func NewCodergenRouter(cfg *RunConfigFile, catalog *modeldb.LiteLLMCatalog) *CodergenRouter {
	return NewCodergenRouterWithRuntimes(cfg, catalog, nil)
}

func NewCodergenRouterWithRuntimes(cfg *RunConfigFile, catalog *modeldb.LiteLLMCatalog, runtimes map[string]ProviderRuntime) *CodergenRouter {
	return &CodergenRouter{
		cfg:              cfg,
		catalog:          catalog,
		providerRuntimes: cloneProviderRuntimeMap(runtimes),
		apiClientFactory: newAPIClientFromProviderRuntimes,
	}
}

func cloneProviderRuntimeMap(in map[string]ProviderRuntime) map[string]ProviderRuntime {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]ProviderRuntime, len(in))
	for k, v := range in {
		cp := v
		cp.Failover = append([]string{}, v.Failover...)
		cp.APIHeadersMap = cloneStringMap(v.APIHeadersMap)
		cp.CLI = cloneCLISpec(v.CLI)
		out[k] = cp
	}
	return out
}

func (r *CodergenRouter) ensureAPIClient() (*llm.Client, error) {
	r.apiOnce.Do(func() {
		// Runtime-driven path (new architecture).
		if len(r.providerRuntimes) > 0 && r.apiClientFactory != nil {
			c, err := r.apiClientFactory(r.providerRuntimes)
			if err != nil {
				r.apiErr = err
				return
			}
			if len(c.ProviderNames()) > 0 {
				r.apiClient = c
				return
			}
			// Empty runtime client is legal for CLI-only runs; preserve old behavior by
			// falling back to env discovery only if API is actually requested.
		}
		r.apiClient, r.apiErr = llmclient.NewFromEnv()
	})
	return r.apiClient, r.apiErr
}

// Migration note: Run() and runAPI() keep their external contracts; only API
// client acquisition changes from api() to ensureAPIClient().
func (r *CodergenRouter) runAPI(ctx context.Context, execCtx *Execution, node *model.Node, provider string, modelID string, prompt string) (string, *runtime.Outcome, error) {
	client, err := r.ensureAPIClient()
	if err != nil {
		return "", nil, err
	}
	// Remaining runAPI logic stays as-is.
	...
}

func profileForRuntimeProvider(rt ProviderRuntime, model string) (agent.ProviderProfile, error) {
	family := strings.TrimSpace(rt.ProfileFamily)
	if family == "" {
		family = rt.Key
	}
	return agent.NewProfileForFamily(family, model)
}

func failoverOrderFromRuntime(primary string, rt map[string]ProviderRuntime) []string {
	p := providerspec.CanonicalProviderKey(primary)
	if r, ok := rt[p]; ok && len(r.Failover) > 0 {
		return append([]string{}, r.Failover...)
	}
	return nil
}

func pickFailoverModelFromRuntime(provider string, rt map[string]ProviderRuntime, catalog *modeldb.LiteLLMCatalog, fallbackModel string) string {
	p := providerspec.CanonicalProviderKey(provider)
	if p == "" {
		return strings.TrimSpace(fallbackModel)
	}
	if m := strings.TrimSpace(pickFailoverModel(p, catalog)); m != "" {
		return m
	}
	ids := modelIDsForProvider(catalog, p)
	if len(ids) > 0 {
		return providerModelIDFromCatalogKey(p, ids[0])
	}
	return strings.TrimSpace(fallbackModel)
}

// Keep existing signature so callers in runAPI(one_shot/agent_loop) stay valid.
func (r *CodergenRouter) withFailoverText(
	ctx context.Context,
	execCtx *Execution,
	node *model.Node,
	client *llm.Client,
	primaryProvider string,
	primaryModel string,
	attempt func(provider string, model string) (string, error),
) (string, providerModel, error) {
	primaryProvider = normalizeProviderKey(primaryProvider)
	primaryModel = strings.TrimSpace(primaryModel)
	runtimeOrder := failoverOrderFromRuntime(primaryProvider, r.providerRuntimes)

	available := map[string]bool{}
	if client != nil {
		for _, p := range client.ProviderNames() {
			available[normalizeProviderKey(p)] = true
		}
	}

	cands := []providerModel{{Provider: primaryProvider, Model: primaryModel}}
	order := runtimeOrder
	if len(order) == 0 {
		order = failoverOrder(primaryProvider)
	}
	for _, p := range order {
		p = normalizeProviderKey(p)
		if p == "" || p == primaryProvider {
			continue
		}
		if r.backendForProvider(p) != BackendAPI {
			continue
		}
		if len(available) > 0 && !available[p] {
			continue
		}
		m := pickFailoverModelFromRuntime(p, r.providerRuntimes, r.catalog, primaryModel)
		if strings.TrimSpace(m) == "" {
			continue
		}
		cands = append(cands, providerModel{Provider: p, Model: m})
	}

	// Existing retry/failover loop continues unchanged.
	...
}
```

```go
// run_with_config.go wiring migration (constructor call site update)
runtimes, err := resolveProviderRuntimes(cfg)
if err != nil {
	return nil, err
}
eng.CodergenBackend = NewCodergenRouterWithRuntimes(cfg, catalog, runtimes)
```

Constructor/behavior notes:
- `ensureAPIClient()` keeps `sync.Once` and remains lazy, so CLI-only runs do not fail during router construction.
- `withFailoverText()` keeps the current signature and return type; call sites in both `one_shot` and `agent_loop` remain unchanged.
- `Run()` path migration is explicit: only constructor usage and API client acquisition change.
- Remove the old `api()` helper after call sites move to `ensureAPIClient()`.

Compatibility contract:
- Kimi/Z use `profile_family: openai`.
- Task 5 verifies OpenAI-style tool-call decoding on Kimi payloads.
- Task 9 verifies end-to-end transport/path behavior for both Kimi and Z.

**Step 4: Run test to verify it passes**

Run: `go test ./internal/agent ./internal/attractor/engine -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/agent/profile_registry.go internal/agent/profile_test.go internal/attractor/engine/codergen_router.go internal/attractor/engine/codergen_failover_test.go internal/attractor/engine/run_with_config.go
git commit -m "refactor(engine): drive API profile selection and failover from runtime provider metadata"
```

### Task 7: Refactor CLI Execution and Preflight to CLI Contracts

**Files:**
- Modify: `internal/attractor/engine/provider_exec_policy.go`
- Modify: `internal/attractor/engine/provider_preflight.go`
- Modify: `internal/attractor/engine/provider_error_classification.go`
- Modify: `internal/attractor/engine/codergen_router.go`
- Test: `internal/attractor/engine/provider_preflight_test.go`
- Test: `internal/attractor/engine/provider_exec_policy_test.go`
- Test: `internal/attractor/engine/provider_error_classification_test.go`

**Step 1: Write the failing test**

```go
func TestDefaultCLIInvocation_UsesSpecTemplate(t *testing.T) {
	spec := providerspec.CLISpec{DefaultExecutable: "mycli", InvocationTemplate: []string{"run", "--model", "{{model}}", "--cwd", "{{worktree}}", "--prompt", "{{prompt}}"}}
	exe, args := materializeCLIInvocation(spec, "m1", "/tmp/w", "fix bug")
	if exe != "mycli" || strings.Join(args, " ") != "run --model m1 --cwd /tmp/w --prompt fix bug" {
		t.Fatalf("materialization mismatch: exe=%s args=%v", exe, args)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/attractor/engine -run TestDefaultCLIInvocation_UsesSpecTemplate -v`
Expected: FAIL (`materializeCLIInvocation` undefined)

**Step 3: Write minimal implementation**

```go
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
		args = append(args, repl)
	}
	return exe, args
}
```

```go
// provider_preflight.go
func missingCapabilityTokensFromSpec(spec *providerspec.CLISpec, helpOutput string) []string {
	if spec == nil {
		return nil
	}
	missing := []string{}
	for _, tok := range spec.CapabilityAll {
		if !strings.Contains(helpOutput, tok) {
			missing = append(missing, tok)
		}
	}
	for _, anyGroup := range spec.CapabilityAnyOf {
		found := false
		for _, tok := range anyGroup {
			if strings.Contains(helpOutput, tok) {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, strings.Join(anyGroup, "|"))
		}
	}
	return missing
}

func probeOutputLooksLikeHelpFromSpec(spec *providerspec.CLISpec, output string) bool {
	if spec == nil || len(spec.CapabilityAll) == 0 {
		return strings.Contains(strings.ToLower(output), "usage")
	}
	for _, tok := range spec.CapabilityAll {
		if strings.Contains(output, tok) {
			return true
		}
	}
	return strings.Contains(strings.ToLower(output), "usage")
}
```

```go
// provider_error_classification.go
func classifyProviderCLIErrorWithContract(provider string, spec *providerspec.CLISpec, stderr string, runErr error) providerCLIClassifiedError {
	if isExecutableNotFound(runErr) {
		return providerCLIClassifiedError{Kind: providerCLIErrorKindExecutableMissing, Message: "provider executable not found"}
	}
	if spec != nil && !probeOutputLooksLikeHelpFromSpec(spec, stderr) && strings.Contains(stderr, "unknown option") {
		return providerCLIClassifiedError{Kind: providerCLIErrorKindCapabilityMissing, Message: "provider CLI missing required capability flags"}
	}
	return providerCLIClassifiedError{Kind: providerCLIErrorKindUnknown, Message: strings.TrimSpace(stderr)}
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/attractor/engine -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/attractor/engine/provider_exec_policy.go internal/attractor/engine/provider_preflight.go internal/attractor/engine/provider_error_classification.go internal/attractor/engine/codergen_router.go internal/attractor/engine/provider_preflight_test.go internal/attractor/engine/provider_exec_policy_test.go internal/attractor/engine/provider_error_classification_test.go
git commit -m "refactor(engine-cli): replace provider-name switches with CLI contract metadata"
```

### Task 8: Wire Kimi and Z as API-Only Providers End-to-End

**Files:**
- Modify: `internal/attractor/engine/run_with_config.go`
- Modify: `internal/attractor/engine/provider_preflight.go`
- Test: `internal/attractor/engine/run_with_config_test.go`
- Test: `internal/attractor/engine/provider_preflight_test.go`

**Step 1: Write the failing test**

```go
func writeProviderCatalogForTest(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "catalog.json")
	if err := os.WriteFile(p, []byte(`{
  "kimi-k2.5": {"litellm_provider":"kimi","mode":"chat"},
  "glm-4.7": {"litellm_provider":"zai","mode":"chat"}
}`), 0o644); err != nil {
		t.Fatalf("write catalog: %v", err)
	}
	return p
}

func TestRunWithConfig_AcceptsKimiAndZaiAPIProviders(t *testing.T) {
	repo := initTestRepo(t)
	cxdbSrv := newCXDBTestServer(t)
	catalogPath := writeProviderCatalogForTest(t)

	cases := []struct {
		provider string
		model    string
		keyEnv   string
		path     string
	}{
		{provider: "kimi", model: "kimi-k2.5", keyEnv: "KIMI_API_KEY", path: "/v1/chat/completions"},
		{provider: "zai", model: "glm-4.7", keyEnv: "ZAI_API_KEY", path: "/api/paas/v4/chat/completions"},
	}

	for _, tc := range cases {
		t.Run(tc.provider, func(t *testing.T) {
			dot := []byte(fmt.Sprintf(`
digraph G {
  start [shape=Mdiamond]
  exit  [shape=Msquare]
  a [shape=box, llm_provider=%s, llm_model=%s, prompt="hi"]
  start -> a -> exit
}
`, tc.provider, tc.model))
			cfg := &RunConfigFile{Version: 1}
			cfg.Repo.Path = repo
			cfg.CXDB.BinaryAddr = cxdbSrv.BinaryAddr()
			cfg.CXDB.HTTPBaseURL = cxdbSrv.URL()
			cfg.ModelDB.LiteLLMCatalogPath = catalogPath
			cfg.ModelDB.LiteLLMCatalogUpdatePolicy = "pinned"
			cfg.LLM.Providers = map[string]ProviderConfig{
				tc.provider: {Backend: BackendAPI, API: ProviderAPIConfig{Protocol: "openai_chat_completions", APIKeyEnv: tc.keyEnv, BaseURL: "http://127.0.0.1:1", Path: tc.path, ProfileFamily: "openai"}},
			}
			t.Setenv(tc.keyEnv, "k-test")
			_, err := RunWithConfig(context.Background(), dot, cfg, RunOptions{RunID: "r1-" + tc.provider, LogsRoot: t.TempDir()})
			if err == nil {
				t.Fatalf("expected transport error from fake endpoint, got nil")
			}
			if strings.Contains(err.Error(), "unsupported provider") {
				t.Fatalf("provider should be accepted, got %v", err)
			}
			if strings.Contains(err.Error(), "not found in model catalog") {
				t.Fatalf("provider/model should pass catalog validation, got %v", err)
			}
		})
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/attractor/engine -run TestRunWithConfig_AcceptsKimiAndZaiAPIProviders -v`
Expected: FAIL (still rejects unknown providers)

**Step 3: Write minimal implementation**

```go
// run_with_config.go
runtimes, err := resolveProviderRuntimes(cfg)
if err != nil {
	return nil, err
}

// Backend presence validation now uses resolved runtime definitions.
for p := range usedProviders {
	rt, ok := runtimes[p]
	if !ok || (rt.Backend != BackendAPI && rt.Backend != BackendCLI) {
		return nil, fmt.Errorf("missing llm.providers.%s.backend (Kilroy forbids implicit backend defaults)", p)
	}
}

if err := validateProviderModelPairs(g, runtimes, catalog, opts); err != nil {
	return nil, err
}
if _, err := runProviderCLIPreflight(ctx, g, runtimes, cfg, opts); err != nil {
	return nil, err
}

eng.CodergenBackend = NewCodergenRouterWithRuntimes(cfg, catalog, runtimes)
```

```go
// run_with_config.go (signature update)
func validateProviderModelPairs(g *model.Graph, runtimes map[string]ProviderRuntime, catalog *modeldb.LiteLLMCatalog, opts RunOptions) error {
	if g == nil || catalog == nil {
		return nil
	}
	for _, n := range g.Nodes {
		if n == nil || n.Shape() != "box" {
			continue
		}
		provider := normalizeProviderKey(n.Attr("llm_provider", ""))
		modelID := strings.TrimSpace(n.Attr("llm_model", ""))
		if modelID == "" {
			modelID = strings.TrimSpace(n.Attr("model", ""))
		}
		if provider == "" || modelID == "" {
			continue
		}
		rt, ok := runtimes[provider]
		if !ok {
			return fmt.Errorf("preflight: provider %s missing runtime definition", provider)
		}
		if rt.Backend != BackendCLI && rt.Backend != BackendAPI {
			continue
		}
		if _, forced := forceModelForProvider(opts.ForceModels, provider); forced {
			continue
		}
		if !catalogHasProviderModel(catalog, provider, modelID) {
			return fmt.Errorf("preflight: llm_provider=%s backend=%s model=%s not present in run catalog", provider, rt.Backend, modelID)
		}
	}
	return nil
}
```

```go
// provider_preflight.go
func runProviderCLIPreflight(ctx context.Context, g *model.Graph, runtimes map[string]ProviderRuntime, cfg *RunConfigFile, opts RunOptions) (*providerPreflightReport, error) {
	report := &providerPreflightReport{
		GeneratedAt:         time.Now().UTC().Format(time.RFC3339Nano),
		CLIProfile:          normalizedCLIProfile(cfg),
		AllowTestShim:       opts.AllowTestShim,
		StrictCapabilities:  parseBool(strings.TrimSpace(os.Getenv("KILROY_PREFLIGHT_STRICT_CAPABILITIES")), false),
		CapabilityProbeMode: capabilityProbeMode(),
	}
	defer func() { _ = writePreflightReport(opts.LogsRoot, report) }()

	providers := usedCLIProviders(g, runtimes)
	if len(providers) == 0 {
		report.addCheck(providerPreflightCheck{
			Name:    "provider_cli_presence",
			Status:  preflightStatusPass,
			Message: "no cli providers used by graph",
		})
		return report, nil
	}
	// Existing executable policy + capability probe loop remains unchanged.
	for _, provider := range providers {
		...
	}
	return report, nil
}

func usedCLIProviders(g *model.Graph, runtimes map[string]ProviderRuntime) []string {
	used := map[string]bool{}
	if g == nil {
		return nil
	}
	for _, n := range g.Nodes {
		if n == nil || n.Shape() != "box" {
			continue
		}
		provider := normalizeProviderKey(n.Attr("llm_provider", ""))
		if provider == "" {
			continue
		}
		rt, ok := runtimes[provider]
		if !ok || rt.Backend != BackendCLI {
			continue
		}
		used[provider] = true
	}
	out := make([]string, 0, len(used))
	for provider := range used {
		out = append(out, provider)
	}
	sort.Strings(out)
	return out
}
```

Catalog contract note:
- No provider/model validation bypass is introduced. `kimi`/`zai` runs still require catalog entries where `litellm_provider` matches canonical provider keys (`kimi`, `zai`).
- The Task 8 test uses a pinned local catalog to make that requirement explicit and deterministic.

**Step 4: Run test to verify it passes**

Run: `go test ./internal/attractor/engine -run TestRunWithConfig_AcceptsKimiAndZaiAPIProviders -v`
Expected: PASS (or deterministic network failure not provider validation failure)

**Step 5: Commit**

```bash
git add internal/attractor/engine/run_with_config.go internal/attractor/engine/provider_preflight.go internal/attractor/engine/run_with_config_test.go internal/attractor/engine/provider_preflight_test.go
git commit -m "feat(engine): accept kimi and zai API providers via runtime provider configuration"
```

### Task 9: Add Integration Tests for Kimi and Z API Protocols

**Files:**
- Create: `internal/attractor/engine/kimi_zai_api_integration_test.go`
- Create (if missing): `internal/attractor/engine/test_helpers_test.go`

**Step 1: Write the failing test**

```go
// Explicit helper provenance:
// - newCXDBTestServer: internal/attractor/engine/cxdb_test_server_test.go
// - initTestRepo:      internal/attractor/engine/run_with_config_integration_test.go
var (
	_ = newCXDBTestServer
	_ = initTestRepo
)

func TestKimiAndZai_OpenAIChatCompletionsIntegration(t *testing.T) {
	var seenPaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPaths = append(seenPaths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","model":"m","choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer srv.Close()

	// configure kimi and zai providers, run tiny graph for each, assert paths observed
	// kimi path: /v1/chat/completions
	// zai path: /api/paas/v4/chat/completions
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/attractor/engine -run TestKimiAndZai_OpenAIChatCompletionsIntegration -v`
Expected: FAIL (new integration test not yet implemented)

**Step 3: Write minimal implementation**

```go
// test_helpers_test.go (only if helper defs are missing in package engine)
// Move/copy canonical helpers so integration tests compile in isolation:
// - func initTestRepo(t *testing.T) string
// - func newCXDBTestServer(t *testing.T) *cxdbTestServer
```

```go
func TestKimiAndZai_OpenAIChatCompletionsIntegration(t *testing.T) {
	repo := initTestRepo(t)
	logsRoot := t.TempDir()
	pinned := writeProviderCatalogForTest(t)
	cxdbSrv := newCXDBTestServer(t)

	var mu sync.Mutex
	seenPaths := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seenPaths[r.URL.Path]++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","model":"m","choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer srv.Close()

	runCase := func(provider, model, keyEnv, path string) {
		t.Helper()
		cfg := &RunConfigFile{Version: 1}
		cfg.Repo.Path = repo
		cfg.CXDB.BinaryAddr = cxdbSrv.BinaryAddr()
		cfg.CXDB.HTTPBaseURL = cxdbSrv.URL()
		cfg.ModelDB.LiteLLMCatalogPath = pinned
		cfg.ModelDB.LiteLLMCatalogUpdatePolicy = "pinned"
		cfg.Git.RunBranchPrefix = "attractor/run"
		cfg.LLM.Providers = map[string]ProviderConfig{
			provider: {Backend: BackendAPI, API: ProviderAPIConfig{
				Protocol:      "openai_chat_completions",
				APIKeyEnv:     keyEnv,
				BaseURL:       srv.URL,
				Path:          path,
				ProfileFamily: "openai",
			}},
		}
		t.Setenv(keyEnv, "k")

		dot := []byte(fmt.Sprintf(`
digraph G {
  start [shape=Mdiamond]
  exit  [shape=Msquare]
  a [shape=box, llm_provider=%s, llm_model=%s, codergen_mode=one_shot, prompt="say hi"]
  start -> a -> exit
}
`, provider, model))

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, err := RunWithConfig(ctx, dot, cfg, RunOptions{RunID: "kz-" + provider, LogsRoot: logsRoot})
		if err != nil {
			t.Fatalf("%s run failed: %v", provider, err)
		}
	}

	runCase("kimi", "kimi-k2.5", "KIMI_API_KEY", "/v1/chat/completions")
	runCase("zai", "glm-4.7", "ZAI_API_KEY", "/api/paas/v4/chat/completions")

	mu.Lock()
	defer mu.Unlock()
	if seenPaths["/v1/chat/completions"] == 0 {
		t.Fatalf("missing kimi chat-completions call: %v", seenPaths)
	}
	if seenPaths["/api/paas/v4/chat/completions"] == 0 {
		t.Fatalf("missing zai chat-completions call: %v", seenPaths)
	}
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/attractor/engine -run TestKimiAndZai_OpenAIChatCompletionsIntegration -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/attractor/engine/kimi_zai_api_integration_test.go internal/attractor/engine/test_helpers_test.go
git commit -m "test(engine): add end-to-end api integration coverage for kimi and zai chat-completions providers"
```

### Task 10: Update Docs, Examples, and Migration Notes

**Files:**
- Modify: `README.md`
- Modify: `docs/strongdm/attractor/README.md`
- Modify: `docs/strongdm/attractor/kilroy-metaspec.md`
- Create: `docs/strongdm/attractor/provider-plugin-migration.md`

**Step 1: Write the failing docs consistency check**

```bash
rg -n "unsupported provider in config|supported providers[^\\n]*openai[^\\n]*anthropic[^\\n]*google|openai\\|anthropic\\|google only" README.md docs/strongdm/attractor/*.md
```

Expected: existing hard-coded wording still present

**Step 2: Run docs check to verify mismatch exists**

Run: `rg -n "supported providers|provider plug-in|protocol" README.md docs/strongdm/attractor/*.md`
Expected: lines requiring update found

**Step 3: Write minimal documentation updates**

```yaml
llm:
  providers:
    kimi:
      backend: api
      api:
        protocol: openai_chat_completions
        api_key_env: KIMI_API_KEY
        base_url: https://api.moonshot.ai
        path: /v1/chat/completions
        profile_family: openai
    zai:
      backend: api
      api:
        protocol: openai_chat_completions
        api_key_env: ZAI_API_KEY
        base_url: https://api.z.ai
        path: /api/paas/v4/chat/completions
        profile_family: openai
```

**Step 4: Run docs check to verify it passes**

Run: `rg -n "unsupported provider in config|openai\|anthropic\|google only" README.md docs/strongdm/attractor/*.md`
Expected: no stale hard-coded-provider claim remains (`rg` exits non-zero)

**Step 5: Commit**

```bash
git add README.md docs/strongdm/attractor/README.md docs/strongdm/attractor/kilroy-metaspec.md docs/strongdm/attractor/provider-plugin-migration.md
git commit -m "docs(attractor): document provider plugin schema and kimi/zai api-only configuration"
```

### Task 11: Final Verification and Safety Regression Sweep

**Files:**
- Modify (if needed): affected tests/docs from previous tasks

**Step 1: Write failing regression test for compatibility (if missing)**

```go
func TestBackwardCompatibility_OpenAIAnthropicGoogleStillValid(t *testing.T) {
	cfg := &RunConfigFile{Version: 1}
	cfg.Repo.Path = "/tmp/repo"
	cfg.CXDB.BinaryAddr = "127.0.0.1:9009"
	cfg.CXDB.HTTPBaseURL = "http://127.0.0.1:9010"
	cfg.ModelDB.LiteLLMCatalogPath = "/tmp/catalog.json"
	cfg.LLM.Providers = map[string]ProviderConfig{
		"openai":    {Backend: BackendAPI},
		"anthropic": {Backend: BackendAPI},
		"google":    {Backend: BackendAPI},
	}
	if err := validateConfig(cfg); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
}
```

Implementation note:
- Keep this test in package `engine` (not `engine_test`) so unexported `validateConfig` remains directly callable.

**Step 2: Run test to verify it fails (if behavior regressed)**

Run: `go test ./internal/attractor/engine -run TestBackwardCompatibility_OpenAIAnthropicGoogleStillValid -v`
Expected: PASS after fixes (if FAIL, fix before final commit)

**Step 3: Run focused and broad test suites**

Run: `go test ./internal/providerspec ./internal/llm/... ./internal/llmclient ./internal/agent ./internal/attractor/engine -count=1`
Expected: PASS

**Step 4: Run formatting/lint checks used by repo**

Run: `go test ./...`
Expected: PASS

**Step 5: Final commit**

```bash
git add $(git diff --name-only)
git commit -m "refactor(attractor): introduce protocol-driven provider plugin architecture and add kimi/zai api support"
```

---

## Notes for Execution

- Keep changes backward compatible until Task 11 (do not break existing `openai/anthropic/google` runs mid-refactor).
- Prefer incremental adapters and wrapper constructors over rewriting all provider code in one commit.
- For API-only rollout, Kimi and Z should be configured with `backend: api`; do not add CLI mappings for them in this pass unless explicitly requested.
- If any task requires unexpected spec decisions (for example custom auth headers beyond bearer), pause and record decision in `docs/strongdm/attractor/provider-plugin-migration.md` before continuing.
