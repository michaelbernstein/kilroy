package engine

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/strongdm/kilroy/internal/attractor/model"
	"github.com/strongdm/kilroy/internal/attractor/modeldb"
	"github.com/strongdm/kilroy/internal/llm"
	"github.com/strongdm/kilroy/internal/providerspec"
)

type okAdapter struct{ name string }

func (a *okAdapter) Name() string { return a.name }
func (a *okAdapter) Complete(ctx context.Context, req llm.Request) (llm.Response, error) {
	_ = ctx
	return llm.Response{Provider: a.name, Model: req.Model, Message: llm.Assistant("ok")}, nil
}
func (a *okAdapter) Stream(ctx context.Context, req llm.Request) (llm.Stream, error) {
	_ = ctx
	_ = req
	return nil, fmt.Errorf("stream not implemented")
}

func TestCodergenRouter_WithFailoverText_FailsOverToDifferentProvider(t *testing.T) {
	cfg := &RunConfigFile{Version: 1}
	cfg.LLM.Providers = map[string]ProviderConfig{
		"openai":      {Backend: BackendAPI},
		"anthropic":   {Backend: BackendAPI},
		"google":      {Backend: BackendAPI},
		"gemini":      {Backend: BackendAPI}, // alias should be normalized away
		"unsupported": {Backend: BackendAPI},
	}
	// Only "openai|anthropic|google" are recognized by normalizeProviderKey; others are ignored by withFailoverText.

	catalog := &modeldb.Catalog{
		Models: map[string]modeldb.ModelEntry{
			// Include a region-prefixed model key to validate providerModelIDFromCatalogKey stripping.
			"us/claude-opus-4-6-20260205": {Provider: "anthropic", Mode: "chat"},
		},
	}

	r := NewCodergenRouter(cfg, catalog)

	client := llm.NewClient()
	client.Register(&okAdapter{name: "openai"})
	client.Register(&okAdapter{name: "anthropic"})

	node := &model.Node{ID: "stage-a"}

	// Capture noisy failover output for determinism.
	oldStderr := os.Stderr
	pr, pw, _ := os.Pipe()
	os.Stderr = pw
	defer func() { os.Stderr = oldStderr }()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	txt, used, err := r.withFailoverText(ctx, nil, node, client, "openai", "gpt-5.2-codex", func(prov string, mid string) (string, error) {
		if prov == "openai" {
			return "", fmt.Errorf("synthetic openai failure")
		}
		if prov == "anthropic" {
			if mid != "claude-opus-4-6-20260205" {
				return "", fmt.Errorf("unexpected fallback model: %q", mid)
			}
			return "ok-from-anthropic", nil
		}
		return "", fmt.Errorf("unexpected provider: %q", prov)
	})

	_ = pw.Close()
	_, _ = io.ReadAll(pr)

	if err != nil {
		t.Fatalf("withFailoverText error: %v", err)
	}
	if txt != "ok-from-anthropic" {
		t.Fatalf("text: got %q", txt)
	}
	if used.Provider != "anthropic" {
		t.Fatalf("used provider: got %q want %q", used.Provider, "anthropic")
	}
	if used.Model != "claude-opus-4-6-20260205" {
		t.Fatalf("used model: got %q want %q", used.Model, "claude-opus-4-6-20260205")
	}
}

func TestCodergenRouter_WithFailoverText_AppliesForceModelToFailoverProvider(t *testing.T) {
	cfg := &RunConfigFile{Version: 1}
	cfg.LLM.Providers = map[string]ProviderConfig{
		"openai":    {Backend: BackendAPI},
		"anthropic": {Backend: BackendAPI},
	}
	catalog := &modeldb.Catalog{
		Models: map[string]modeldb.ModelEntry{
			"us/claude-opus-4-6-20260205": {Provider: "anthropic", Mode: "chat"},
		},
	}

	r := NewCodergenRouter(cfg, catalog)
	client := llm.NewClient()
	client.Register(&okAdapter{name: "openai"})
	client.Register(&okAdapter{name: "anthropic"})

	node := &model.Node{ID: "stage-a"}
	execCtx := &Execution{
		Engine: &Engine{
			Options: RunOptions{
				ForceModels: map[string]string{"anthropic": "claude-force-override"},
			},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	txt, used, err := r.withFailoverText(ctx, execCtx, node, client, "openai", "gpt-5.2-codex", func(prov string, mid string) (string, error) {
		if prov == "openai" {
			return "", fmt.Errorf("synthetic openai failure")
		}
		if prov == "anthropic" {
			if mid != "claude-force-override" {
				return "", fmt.Errorf("unexpected fallback model: %q", mid)
			}
			return "ok-from-anthropic-force", nil
		}
		return "", fmt.Errorf("unexpected provider: %q", prov)
	})
	if err != nil {
		t.Fatalf("withFailoverText error: %v", err)
	}
	if txt != "ok-from-anthropic-force" {
		t.Fatalf("text: got %q", txt)
	}
	if used.Provider != "anthropic" {
		t.Fatalf("used provider: got %q want %q", used.Provider, "anthropic")
	}
	if used.Model != "claude-force-override" {
		t.Fatalf("used model: got %q want %q", used.Model, "claude-force-override")
	}
}

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
		"openai": {
			Key:     "openai",
			Backend: BackendAPI,
			API: providerspec.APISpec{
				Protocol: providerspec.ProtocolOpenAIResponses,
			},
		},
	})
	r.apiClientFactory = func(map[string]ProviderRuntime) (*llm.Client, error) {
		calls.Add(1)
		c := llm.NewClient()
		c.Register(&okAdapter{name: "openai"})
		return c, nil
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
