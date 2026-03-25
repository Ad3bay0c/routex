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

func anthropicToolCallContent(calls []map[string]any) map[string]any {
	return map[string]any{
		"id":          "msg_test",
		"type":        "message",
		"role":        "assistant",
		"content":     calls,
		"model":       "claude-sonnet-4-6",
		"stop_reason": "tool_use",
		"usage": map[string]any{
			"input_tokens":  20,
			"output_tokens": 15,
		},
	}
}

func anthropicToolUseBlock(id, name string, input map[string]any) map[string]any {
	return map[string]any{
		"type":  "tool_use",
		"id":    id,
		"name":  name,
		"input": input,
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
	if len(resp.ToolCalls) != 0 {
		t.Errorf("ToolCalls should be empty for text response, got %d", len(resp.ToolCalls))
	}
	if resp.Usage.InputTokens != 12 {
		t.Errorf("InputTokens = %d, want 12", resp.Usage.InputTokens)
	}
	if resp.Usage.OutputTokens != 25 {
		t.Errorf("OutputTokens = %d, want 25", resp.Usage.OutputTokens)
	}
	if resp.FinishReason != "end_turn" {
		t.Errorf("FinishReason = %q, want end_turn", resp.FinishReason)
	}
}

func TestAnthropicAdapter_SingleToolCall(t *testing.T) {
	toolID := "tool_weather_456"
	toolName := "web_search"
	srv := mockServer(t, http.StatusOK, anthropicToolCallContent([]map[string]any{
		anthropicToolUseBlock(toolID, toolName, map[string]any{"query": "Lagos weather today"}),
	}))

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
			toolName: {Description: "Search the web"},
		},
	})
	if err != nil {
		t.Fatalf("Complete() error: %v", err)
	}

	if resp.Content != "" {
		t.Errorf("Content should be empty for tool call, got %q", resp.Content)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("ToolCalls len = %d, want 1", len(resp.ToolCalls))
	}
	tc := resp.ToolCalls[0]
	if tc.ToolName != toolName {
		t.Errorf("ToolName = %q, want %q", tc.ToolName, toolName)
	}
	if tc.ID != toolID {
		t.Errorf("ID = %q, want toolu_test_456", tc.ID)
	}

	var parsedInput map[string]any
	if err := json.Unmarshal([]byte(tc.Input), &parsedInput); err != nil {
		t.Fatalf("Input is not valid JSON: %v", err)
	}
	if parsedInput["query"] != "Lagos weather today" {
		t.Errorf("Input query = %q, want Lagos weather today", parsedInput["query"])
	}
}

func TestAnthropicAdapter_MultipleToolCalls(t *testing.T) {
	// Anthropic returns multiple tool_use blocks in one response
	srv := mockServer(t, http.StatusOK, anthropicToolCallContent([]map[string]any{
		anthropicToolUseBlock("toolu_1", "web_search", map[string]any{"query": "Go 1.24"}),
		anthropicToolUseBlock("toolu_2", "read_file", map[string]any{"path": "notes.md"}),
		anthropicToolUseBlock("toolu_3", "wikipedia", map[string]any{"topic": "Go language"}),
	}))

	adapter, err := NewAnthropicAdapter(Config{
		APIKey: "test-key", Model: "claude-sonnet-4-6",
		BaseURL: srv.URL, Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewAnthropicAdapter() error: %v", err)
	}

	resp, err := adapter.Complete(context.Background(), simpleRequest("research Go"))
	if err != nil {
		t.Fatalf("Complete() error: %v", err)
	}

	if len(resp.ToolCalls) != 3 {
		t.Fatalf("ToolCalls len = %d, want 3", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].ToolName != "web_search" {
		t.Errorf("[0].ToolName = %q, want web_search", resp.ToolCalls[0].ToolName)
	}
	if resp.ToolCalls[1].ToolName != "read_file" {
		t.Errorf("[1].ToolName = %q, want read_file", resp.ToolCalls[1].ToolName)
	}
	if resp.ToolCalls[2].ToolName != "wikipedia" {
		t.Errorf("[2].ToolName = %q, want wikipedia", resp.ToolCalls[2].ToolName)
	}
	// IDs preserved — used to match tool_result back
	if resp.ToolCalls[0].ID != "toolu_1" {
		t.Errorf("[0].ID = %q, want toolu_1", resp.ToolCalls[0].ID)
	}
	if resp.ToolCalls[2].ID != "toolu_3" {
		t.Errorf("[2].ID = %q, want toolu_3", resp.ToolCalls[2].ID)
	}
}

func TestAnthropicAdapter_ErrorResponse(t *testing.T) {
	srv := mockServer(t, http.StatusUnauthorized, map[string]any{
		"type":  "error",
		"error": map[string]any{"type": "authentication_error", "message": "invalid api key"},
	})

	adapter, err := NewAnthropicAdapter(Config{
		APIKey: "bad-key", Model: "claude-sonnet-4-6",
		BaseURL: srv.URL, Timeout: 5 * time.Second,
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
	a := &AnthropicAdapter{model: "claude-sonnet-4-6"}
	if a.Model() != "claude-sonnet-4-6" {
		t.Errorf("Model() = %q, want claude-sonnet-4-6", a.Model())
	}
	if a.Provider() != "anthropic" {
		t.Errorf("Provider() = %q, want anthropic", a.Provider())
	}
}

func TestAnthropicAdapter_MissingAPIKey(t *testing.T) {
	_, err := NewAnthropicAdapter(Config{Model: "claude-sonnet-4-6"})
	if err == nil {
		t.Fatal("NewAnthropicAdapter() should error with empty APIKey")
	}
}
