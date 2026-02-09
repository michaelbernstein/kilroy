package engine

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/strongdm/kilroy/internal/llm"
)

type preflightProbeTestAdapter struct {
	name       string
	completeFn func(ctx context.Context, req llm.Request) (llm.Response, error)
}

func (a preflightProbeTestAdapter) Name() string { return a.name }

func (a preflightProbeTestAdapter) Complete(ctx context.Context, req llm.Request) (llm.Response, error) {
	if a.completeFn == nil {
		return llm.Response{}, fmt.Errorf("completeFn is nil")
	}
	return a.completeFn(ctx, req)
}

func (a preflightProbeTestAdapter) Stream(ctx context.Context, req llm.Request) (llm.Stream, error) {
	return nil, fmt.Errorf("stream not implemented in preflight probe test adapter")
}

func TestRunProviderAPIPromptProbe_RetriesRequestTimeoutAndSucceeds(t *testing.T) {
	t.Setenv("KILROY_PREFLIGHT_API_PROMPT_PROBE_TIMEOUT_MS", "100")
	t.Setenv("KILROY_PREFLIGHT_API_PROMPT_PROBE_RETRIES", "2")
	t.Setenv("KILROY_PREFLIGHT_API_PROMPT_PROBE_BASE_DELAY_MS", "1")
	t.Setenv("KILROY_PREFLIGHT_API_PROMPT_PROBE_MAX_DELAY_MS", "5")

	var calls atomic.Int32
	client := llm.NewClient()
	client.Register(preflightProbeTestAdapter{
		name: "zai",
		completeFn: func(ctx context.Context, req llm.Request) (llm.Response, error) {
			if calls.Add(1) == 1 {
				return llm.Response{}, llm.NewRequestTimeoutError("zai", "context deadline exceeded")
			}
			return llm.Response{
				Provider: "zai",
				Model:    req.Model,
				Message:  llm.Assistant("OK"),
			}, nil
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	text, err := runProviderAPIPromptProbe(ctx, client, "zai", "glm-4.7")
	if err != nil {
		t.Fatalf("runProviderAPIPromptProbe: %v", err)
	}
	if text != "OK" {
		t.Fatalf("probe text=%q want %q", text, "OK")
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("retry calls=%d want 2", got)
	}
}

func TestRunProviderAPIPromptProbe_DoesNotRetryInvalidRequest(t *testing.T) {
	t.Setenv("KILROY_PREFLIGHT_API_PROMPT_PROBE_TIMEOUT_MS", "100")
	t.Setenv("KILROY_PREFLIGHT_API_PROMPT_PROBE_RETRIES", "3")
	t.Setenv("KILROY_PREFLIGHT_API_PROMPT_PROBE_BASE_DELAY_MS", "1")
	t.Setenv("KILROY_PREFLIGHT_API_PROMPT_PROBE_MAX_DELAY_MS", "5")

	var calls atomic.Int32
	client := llm.NewClient()
	client.Register(preflightProbeTestAdapter{
		name: "zai",
		completeFn: func(ctx context.Context, req llm.Request) (llm.Response, error) {
			calls.Add(1)
			return llm.Response{}, llm.ErrorFromHTTPStatus("zai", 400, "invalid_request_error", nil, nil)
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := runProviderAPIPromptProbe(ctx, client, "zai", "glm-4.7")
	if err == nil {
		t.Fatalf("expected probe error, got nil")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("invalid-request calls=%d want 1", got)
	}
}
