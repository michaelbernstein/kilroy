package openaicompat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/strongdm/kilroy/internal/llm"
)

func TestAdapter_Complete_ChatCompletionsMapsToolCalls(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"id":"c1","model":"m","choices":[{"finish_reason":"tool_calls","message":{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"read_file","arguments":"{\"file_path\":\"README.md\"}"}}]}}],"usage":{"prompt_tokens":10,"completion_tokens":3,"total_tokens":13}}`))
	}))
	defer srv.Close()

	a := NewAdapter(Config{
		Provider:   "kimi",
		APIKey:     "k",
		BaseURL:    srv.URL,
		Path:       "/v1/chat/completions",
		OptionsKey: "kimi",
	})
	resp, err := a.Complete(context.Background(), llm.Request{
		Provider: "kimi",
		Model:    "kimi-k2.5",
		Messages: []llm.Message{llm.User("hi")},
	})
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
	stream, err := a.Stream(context.Background(), llm.Request{
		Provider: "zai",
		Model:    "glm-4.7",
		Messages: []llm.Message{llm.User("hi")},
	})
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
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hello\"},\"finish_reason\":\"stop\"}],\n"))
		_, _ = w.Write([]byte("data: \"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}\n\n"))
		_, _ = w.Write([]byte("data: {\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":2,\"total_tokens\":3}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	a := NewAdapter(Config{Provider: "zai", APIKey: "k", BaseURL: srv.URL})
	stream, err := a.Stream(context.Background(), llm.Request{
		Provider: "zai",
		Model:    "glm-4.7",
		Messages: []llm.Message{llm.User("hi")},
	})
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

func TestAdapter_Stream_UsageOnlyChunkPreservesTokenAccounting(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":7,\"total_tokens\":12}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	a := NewAdapter(Config{Provider: "zai", APIKey: "k", BaseURL: srv.URL})
	stream, err := a.Stream(context.Background(), llm.Request{
		Provider: "zai",
		Model:    "glm-4.7",
		Messages: []llm.Message{llm.User("hi")},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer stream.Close()

	var finishUsage llm.Usage
	sawFinish := false
	for ev := range stream.Events() {
		if ev.Type != llm.StreamEventFinish || ev.Usage == nil {
			continue
		}
		sawFinish = true
		finishUsage = *ev.Usage
	}
	if !sawFinish {
		t.Fatalf("expected finish event")
	}
	if finishUsage.TotalTokens != 12 {
		t.Fatalf("usage mismatch: %#v", finishUsage)
	}
}
