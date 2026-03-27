package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Ad3bay0c/routex/tools"
)

func newTestAnthropic(t *testing.T, srv *httptest.Server) *AnthropicAdapter {
	t.Helper()
	a, err := NewAnthropicAdapter(Config{
		APIKey:      "test-key",
		Model:       "claude-sonnet-4-6",
		BaseURL:     srv.URL,
		MaxTokens:   1024,
		Temperature: 0.7,
		Timeout:     5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewAnthropicAdapter: %v", err)
	}
	return a
}

func anthropicTextBody(content string) map[string]any {
	return map[string]any{
		"id": "msg_test", "type": "message", "role": "assistant",
		"content":     []map[string]any{{"type": "text", "text": content}},
		"model":       "claude-sonnet-4-6",
		"stop_reason": "end_turn",
		"usage":       map[string]any{"input_tokens": 12, "output_tokens": 25},
	}
}

func anthropicToolCallBody(calls []map[string]any) map[string]any {
	return map[string]any{
		"id": "msg_test", "type": "message", "role": "assistant",
		"content":     calls,
		"model":       "claude-sonnet-4-6",
		"stop_reason": "tool_use",
		"usage":       map[string]any{"input_tokens": 20, "output_tokens": 15},
	}
}

func anthropicToolUse(id, name string, input map[string]any) map[string]any {
	return map[string]any{"type": "tool_use", "id": id, "name": name, "input": input}
}

func TestAnthropic_MissingAPIKey(t *testing.T) {
	_, err := NewAnthropicAdapter(Config{Model: "claude-sonnet-4-6"})
	if err == nil {
		t.Fatal("should error with empty APIKey")
	}
}

func TestAnthropic_TextResponse(t *testing.T) {
	srv := mockServer(t, http.StatusOK, anthropicTextBody("Hello from Claude"))
	resp, err := newTestAnthropic(t, srv).Complete(context.Background(), simpleRequest("hello"))
	if err != nil {
		t.Fatalf("Complete() error: %v", err)
	}
	if resp.Content != "Hello from Claude" {
		t.Errorf("Content = %q, want Hello from Claude", resp.Content)
	}
	if len(resp.ToolCalls) != 0 {
		t.Errorf("ToolCalls should be empty, got %d", len(resp.ToolCalls))
	}
	if resp.Usage.InputTokens != 12 {
		t.Errorf("InputTokens = %d, want 12", resp.Usage.InputTokens)
	}
	if resp.FinishReason != "end_turn" {
		t.Errorf("FinishReason = %q, want end_turn", resp.FinishReason)
	}
}

func TestAnthropic_SingleToolCall(t *testing.T) {
	srv := mockServer(t, http.StatusOK, anthropicToolCallBody([]map[string]any{
		anthropicToolUse("toolu_456", "web_search", map[string]any{"query": "Lagos weather"}),
	}))

	resp, err := newTestAnthropic(t, srv).Complete(context.Background(), Request{
		SystemPrompt: "You are a researcher.",
		History:      simpleHistory("Lagos weather"),
		ToolSchemas:  map[string]tools.Schema{"web_search": {Description: "Search"}},
	})
	if err != nil {
		t.Fatalf("Complete() error: %v", err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("ToolCalls len = %d, want 1", len(resp.ToolCalls))
	}
	tc := resp.ToolCalls[0]
	if tc.ToolName != "web_search" {
		t.Errorf("ToolName = %q, want web_search", tc.ToolName)
	}
	if tc.ID != "toolu_456" {
		t.Errorf("ID = %q, want toolu_456", tc.ID)
	}
	// Input should be valid JSON
	var parsed map[string]any
	if err := json.Unmarshal([]byte(tc.Input), &parsed); err != nil {
		t.Errorf("Input not valid JSON: %v — got %q", err, tc.Input)
	}
	if parsed["query"] != "Lagos weather" {
		t.Errorf("Input query = %q, want Lagos weather", parsed["query"])
	}
}

func TestAnthropic_MultipleToolCalls(t *testing.T) {
	srv := mockServer(t, http.StatusOK, anthropicToolCallBody([]map[string]any{
		anthropicToolUse("toolu_1", "web_search", map[string]any{"query": "Go"}),
		anthropicToolUse("toolu_2", "read_file", map[string]any{"path": "f.md"}),
		anthropicToolUse("toolu_3", "wikipedia", map[string]any{"topic": "Go"}),
	}))

	resp, err := newTestAnthropic(t, srv).Complete(context.Background(), simpleRequest("research"))
	if err != nil {
		t.Fatalf("Complete() error: %v", err)
	}
	if len(resp.ToolCalls) != 3 {
		t.Fatalf("ToolCalls len = %d, want 3", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].ToolName != "web_search" {
		t.Errorf("[0].ToolName = %q, want web_search", resp.ToolCalls[0].ToolName)
	}
	if resp.ToolCalls[2].ID != "toolu_3" {
		t.Errorf("[2].ID = %q, want toolu_3", resp.ToolCalls[2].ID)
	}
}

func TestAnthropic_ErrorResponse(t *testing.T) {
	srv := mockServer(t, http.StatusUnauthorized, map[string]any{
		"error": map[string]any{"type": "authentication_error", "message": "invalid api key"},
	})
	_, err := newTestAnthropic(t, srv).Complete(context.Background(), simpleRequest("hello"))
	if err == nil {
		t.Fatal("Complete() should error for 401")
	}
}

func TestAnthropic_RequestHeaders(t *testing.T) {
	var apiKey, version, contentType string
	srv := mockServerFunc(t, func(w http.ResponseWriter, r *http.Request) {
		apiKey = r.Header.Get("x-api-key")
		version = r.Header.Get("anthropic-version")
		contentType = r.Header.Get("Content-Type")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(anthropicTextBody("ok"))
	})

	newTestAnthropic(t, srv).Complete(context.Background(), simpleRequest("hello"))

	if apiKey != "test-key" {
		t.Errorf("x-api-key = %q, want test-key", apiKey)
	}
	if version != anthropicVersion {
		t.Errorf("anthropic-version = %q, want %q", version, anthropicVersion)
	}
	if contentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", contentType)
	}
}

func TestAnthropic_ContextCancellation(t *testing.T) {
	ctx := context.Background()
	srv := mockServerFunc(t, func(w http.ResponseWriter, r *http.Request) {
		<-ctx.Done()
	})
	ctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	_, err := newTestAnthropic(t, srv).Complete(ctx, simpleRequest("hello"))
	if err == nil {
		t.Fatal("should error when context cancelled")
	}
}

func TestAnthropic_ModelAndProvider(t *testing.T) {
	a := &AnthropicAdapter{model: "claude-sonnet-4-6"}
	if a.Model() != "claude-sonnet-4-6" {
		t.Errorf("Model() = %q, want claude-sonnet-4-6", a.Model())
	}
	if a.Provider() != "anthropic" {
		t.Errorf("Provider() = %q, want anthropic", a.Provider())
	}
}
