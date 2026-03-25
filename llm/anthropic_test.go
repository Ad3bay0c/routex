package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/Ad3bay0c/routex/tools"
)

func anthropicTextResponse(content string) map[string]any {
	return map[string]any{
		"id":   "msg_test",
		"type": "message",
		"role": "assistant",
		"content": []map[string]any{
			{"type": "text", "text": content},
		},
		"model":       "claude-sonnet-4-6",
		"stop_reason": "end_turn",
		"usage": map[string]any{
			"input_tokens":  12,
			"output_tokens": 25,
		},
	}
}

func anthropicToolCallResponse(toolName, toolID string, input map[string]any) map[string]any {
	return map[string]any{
		"id":   "msg_test",
		"type": "message",
		"role": "assistant",
		"content": []map[string]any{
			{
				"type":  "tool_use",
				"id":    toolID,
				"name":  toolName,
				"input": input,
			},
		},
		"model":       "claude-sonnet-4-6",
		"stop_reason": "tool_use",
		"usage": map[string]any{
			"input_tokens":  20,
			"output_tokens": 15,
		},
	}
}

func TestAnthropicAdapter_TextResponse(t *testing.T) {
	srv := mockServer(t, http.StatusOK, anthropicTextResponse("Hello from mock Claude"))

	adapter, err := NewAnthropicAdapter(Config{
		APIKey:  "test-key",
		Model:   "claude-sonnet-4-6",
		BaseURL: srv.URL,
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewAnthropicAdapter() error: %v", err)
	}

	resp, err := adapter.Complete(context.Background(), simpleRequest("say hello"))
	if err != nil {
		t.Fatalf("Complete() error: %v", err)
	}

	if resp.Content != "Hello from mock Claude" {
		t.Errorf("Content = %q, want %q", resp.Content, "Hello from mock Claude")
	}
	if resp.ToolCall != nil {
		t.Errorf("ToolCall should be nil for text response")
	}
	if resp.Usage.InputTokens != 12 {
		t.Errorf("InputTokens = %d, want 12", resp.Usage.InputTokens)
	}
	if resp.Usage.OutputTokens != 25 {
		t.Errorf("OutputTokens = %d, want 25", resp.Usage.OutputTokens)
	}
	if resp.FinishReason != "end_turn" {
		t.Errorf("FinishReason = %q, want %q", resp.FinishReason, "end_turn")
	}
}

func TestAnthropicAdapter_ToolCallResponse(t *testing.T) {
	toolID := "tool_weather_456"
	toolName := "web_search"
	toolInput := map[string]any{"query": "Lagos weather today"}
	srv := mockServer(t, http.StatusOK,
		anthropicToolCallResponse(toolName, toolID, toolInput),
	)

	adapter, err := NewAnthropicAdapter(Config{
		APIKey:  "test-key",
		Model:   "claude-sonnet-4-6",
		BaseURL: srv.URL,
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewAnthropicAdapter() error: %v", err)
	}

	resp, err := adapter.Complete(context.Background(), Request{
		SystemPrompt: "You are a researcher.",
		History:      simpleHistory("find weather in Lagos"),
		ToolSchemas: map[string]tools.Schema{
			toolName: {
				Description: "Search the web",
				Parameters: map[string]tools.Parameter{
					"query": {Type: "string", Required: true},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Complete() error: %v", err)
	}

	if resp.Content != "" {
		t.Errorf("Content should be empty for tool call, got %q", resp.Content)
	}
	if resp.ToolCall == nil {
		t.Fatalf("ToolCall should not be nil")
	}
	if resp.ToolCall.ToolName != toolName {
		t.Errorf("ToolName = %q, want %q", resp.ToolCall.ToolName, toolName)
	}
	if resp.ToolCall.ID != toolID {
		t.Errorf("ToolCall.ID = %q, want %q", resp.ToolCall.ID, toolID)
	}

	// Input should be the JSON-serialised form of toolInput
	var parsedInput map[string]any
	if err := json.Unmarshal([]byte(resp.ToolCall.Input), &parsedInput); err != nil {
		t.Fatalf("ToolCall.Input is not valid JSON: %v", err)
	}
	if parsedInput["query"] != "Lagos weather today" {
		t.Errorf("ToolCall.Input query = %q, want %q", parsedInput["query"], "Lagos weather today")
	}
}

func TestAnthropicAdapter_ErrorResponse(t *testing.T) {
	srv := mockServer(t, http.StatusUnauthorized, map[string]any{
		"type":  "error",
		"error": map[string]any{"type": "authentication_error", "message": "invalid api key"},
	})

	adapter, err := NewAnthropicAdapter(Config{
		APIKey:  "bad-key",
		Model:   "claude-sonnet-4-6",
		BaseURL: srv.URL,
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewAnthropicAdapter() error: %v", err)
	}

	_, err = adapter.Complete(context.Background(), simpleRequest("hello"))
	if err == nil {
		t.Fatal("Complete() should return error for 401 response")
	}
}

func TestAnthropicAdapter_ModelAndProvider(t *testing.T) {
	adapter := &AnthropicAdapter{model: "claude-sonnet-4-6"}
	if adapter.Model() != "claude-sonnet-4-6" {
		t.Errorf("Model() = %q, want %q", adapter.Model(), "claude-sonnet-4-6")
	}
	if adapter.Provider() != "anthropic" {
		t.Errorf("Provider() = %q, want %q", adapter.Provider(), "anthropic")
	}
}

func TestAnthropicAdapter_MissingAPIKey(t *testing.T) {
	_, err := NewAnthropicAdapter(Config{Model: "claude-sonnet-4-6"})
	if err == nil {
		t.Fatal("NewAnthropicAdapter() should error with empty APIKey")
	}
	if err.Error() != "anthropic: api_key is required" {
		t.Errorf("Error() = %q, want %q", err, "anthropic: api_key is required")
	}
}
