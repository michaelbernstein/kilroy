package engine

import (
	"testing"
)

func TestParseCLIStreamLine_AssistantWithTextAndToolUse(t *testing.T) {
	line := []byte(`{
		"type": "assistant",
		"message": {
			"model": "claude-sonnet-4-5-20250929",
			"id": "msg_123",
			"role": "assistant",
			"content": [
				{"type": "text", "text": "Let me read that file."},
				{"type": "tool_use", "id": "toolu_abc", "name": "Read", "input": {"file_path": "/tmp/foo.go"}}
			],
			"usage": {
				"input_tokens": 1500,
				"output_tokens": 42
			}
		},
		"session_id": "sess-1",
		"uuid": "uuid-1"
	}`)

	ev, err := parseCLIStreamLine(line)
	if err != nil {
		t.Fatalf("parseCLIStreamLine: %v", err)
	}
	if ev.Type != "assistant" {
		t.Fatalf("type: got %q want %q", ev.Type, "assistant")
	}
	if ev.Message == nil {
		t.Fatal("message is nil")
	}
	if ev.Message.Model != "claude-sonnet-4-5-20250929" {
		t.Fatalf("model: got %q", ev.Message.Model)
	}

	text := extractAssistantText(ev.Message)
	if text != "Let me read that file." {
		t.Fatalf("text: got %q", text)
	}

	calls := extractToolCalls(ev.Message)
	if len(calls) != 1 {
		t.Fatalf("tool calls: got %d want 1", len(calls))
	}
	if calls[0].Name != "Read" {
		t.Fatalf("tool name: got %q want %q", calls[0].Name, "Read")
	}
	if calls[0].ID != "toolu_abc" {
		t.Fatalf("tool id: got %q want %q", calls[0].ID, "toolu_abc")
	}

	if ev.Message.Usage == nil {
		t.Fatal("usage is nil")
	}
	if ev.Message.Usage.InputTokens != 1500 {
		t.Fatalf("input_tokens: got %d want 1500", ev.Message.Usage.InputTokens)
	}
	if ev.Message.Usage.OutputTokens != 42 {
		t.Fatalf("output_tokens: got %d want 42", ev.Message.Usage.OutputTokens)
	}
}

func TestParseCLIStreamLine_UserWithToolResult(t *testing.T) {
	line := []byte(`{
		"type": "user",
		"message": {
			"role": "user",
			"content": [
				{
					"tool_use_id": "toolu_abc",
					"type": "tool_result",
					"content": "file contents here"
				}
			]
		},
		"session_id": "sess-1",
		"uuid": "uuid-2"
	}`)

	ev, err := parseCLIStreamLine(line)
	if err != nil {
		t.Fatalf("parseCLIStreamLine: %v", err)
	}
	if ev.Type != "user" {
		t.Fatalf("type: got %q want %q", ev.Type, "user")
	}

	results := extractToolResults(ev.Message)
	if len(results) != 1 {
		t.Fatalf("tool results: got %d want 1", len(results))
	}
	if results[0].ToolUseID != "toolu_abc" {
		t.Fatalf("tool_use_id: got %q", results[0].ToolUseID)
	}
	if results[0].Content != "file contents here" {
		t.Fatalf("content: got %q", results[0].Content)
	}
}

func TestParseCLIStreamLine_SystemAndInitSkipped(t *testing.T) {
	for _, tc := range []struct {
		name string
		line string
	}{
		{"system", `{"type":"system","subtype":"init","session_id":"s1"}`},
		{"result", `{"type":"result","subtype":"success","result":"done"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ev, err := parseCLIStreamLine([]byte(tc.line))
			if err != nil {
				t.Fatalf("parseCLIStreamLine: %v", err)
			}
			if ev.Type != tc.name {
				t.Fatalf("type: got %q want %q", ev.Type, tc.name)
			}
			// These types should have no message to decompose.
			if ev.Message != nil {
				t.Fatalf("expected nil message for type %q", tc.name)
			}
		})
	}
}

func TestParseCLIStreamLine_InvalidJSON(t *testing.T) {
	_, err := parseCLIStreamLine([]byte(`not json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestParseCLIStreamLine_EmptyLine(t *testing.T) {
	ev, err := parseCLIStreamLine([]byte(""))
	if err != nil {
		t.Fatalf("parseCLIStreamLine: %v", err)
	}
	if ev != nil {
		t.Fatalf("expected nil for empty line, got type=%q", ev.Type)
	}
}

func TestExtractToolResults_ErrorResult(t *testing.T) {
	line := []byte(`{
		"type": "user",
		"message": {
			"role": "user",
			"content": [
				{
					"tool_use_id": "toolu_err",
					"type": "tool_result",
					"content": "command failed",
					"is_error": true
				}
			]
		}
	}`)

	ev, err := parseCLIStreamLine(line)
	if err != nil {
		t.Fatalf("parseCLIStreamLine: %v", err)
	}
	results := extractToolResults(ev.Message)
	if len(results) != 1 {
		t.Fatalf("results: got %d want 1", len(results))
	}
	if !results[0].IsError {
		t.Fatal("expected is_error=true")
	}
}
