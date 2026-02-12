// Parses Claude CLI stream-json NDJSON output into structured events
// for decomposition into individual CXDB turns.
package engine

import (
	"bytes"
	"encoding/json"
)

// cliStreamEvent represents a single NDJSON line from Claude CLI --output-format stream-json.
type cliStreamEvent struct {
	Type    string      `json:"type"`
	Message *cliMessage `json:"message,omitempty"`
}

// cliMessage is the "message" field of an assistant or user stream event.
type cliMessage struct {
	Model   string            `json:"model,omitempty"`
	ID      string            `json:"id,omitempty"`
	Role    string            `json:"role,omitempty"`
	Content []cliContentBlock `json:"content,omitempty"`
	Usage   *cliUsage         `json:"usage,omitempty"`
}

// cliContentBlock is a single entry in message.content[].
type cliContentBlock struct {
	Type string `json:"type"`

	// text block
	Text string `json:"text,omitempty"`

	// tool_use block
	ID    string         `json:"id,omitempty"`
	Name  string         `json:"name,omitempty"`
	Input map[string]any `json:"input,omitempty"`

	// tool_result block
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"-"` // handled manually; can be string or array
	IsError   bool   `json:"is_error,omitempty"`
}

// cliUsage holds token counts from the assistant message.
type cliUsage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
}

// cliToolCall is an extracted tool_use block from an assistant message.
type cliToolCall struct {
	ID        string
	Name      string
	InputJSON string
}

// cliToolResult is an extracted tool_result block from a user message.
type cliToolResult struct {
	ToolUseID string
	Content   string
	IsError   bool
}

// parseCLIStreamLine parses a single NDJSON line. Returns nil for empty lines.
func parseCLIStreamLine(line []byte) (*cliStreamEvent, error) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return nil, nil
	}
	var ev cliStreamEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		return nil, err
	}
	// Parse tool_result content blocks manually since "content" can be
	// either a string or an array of objects in the Claude API.
	if ev.Message != nil {
		parseToolResultContent(line, ev.Message)
	}
	return &ev, nil
}

// parseToolResultContent fills in cliContentBlock.Content for tool_result blocks
// by re-parsing the raw JSON. The Claude API emits "content" as either a plain
// string or a structured array; we normalize to a string.
func parseToolResultContent(raw []byte, msg *cliMessage) {
	var envelope struct {
		Message struct {
			Content []json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return
	}
	for i, rawBlock := range envelope.Message.Content {
		if i >= len(msg.Content) {
			break
		}
		if msg.Content[i].Type != "tool_result" {
			continue
		}
		var block struct {
			Content any `json:"content"`
		}
		if err := json.Unmarshal(rawBlock, &block); err != nil {
			continue
		}
		switch v := block.Content.(type) {
		case string:
			msg.Content[i].Content = v
		default:
			// Array or other structured content: serialize back to string.
			b, _ := json.Marshal(v)
			msg.Content[i].Content = string(b)
		}
	}
}

// extractAssistantText concatenates all text blocks from an assistant message.
func extractAssistantText(msg *cliMessage) string {
	if msg == nil {
		return ""
	}
	var buf bytes.Buffer
	for _, block := range msg.Content {
		if block.Type == "text" && block.Text != "" {
			if buf.Len() > 0 {
				buf.WriteByte('\n')
			}
			buf.WriteString(block.Text)
		}
	}
	return buf.String()
}

// extractToolCalls returns all tool_use blocks from an assistant message.
func extractToolCalls(msg *cliMessage) []cliToolCall {
	if msg == nil {
		return nil
	}
	var calls []cliToolCall
	for _, block := range msg.Content {
		if block.Type != "tool_use" {
			continue
		}
		inputJSON := ""
		if block.Input != nil {
			b, _ := json.Marshal(block.Input)
			inputJSON = string(b)
		}
		calls = append(calls, cliToolCall{
			ID:        block.ID,
			Name:      block.Name,
			InputJSON: inputJSON,
		})
	}
	return calls
}

// extractToolResults returns all tool_result blocks from a user message.
func extractToolResults(msg *cliMessage) []cliToolResult {
	if msg == nil {
		return nil
	}
	var results []cliToolResult
	for _, block := range msg.Content {
		if block.Type != "tool_result" {
			continue
		}
		results = append(results, cliToolResult{
			ToolUseID: block.ToolUseID,
			Content:   block.Content,
			IsError:   block.IsError,
		})
	}
	return results
}
