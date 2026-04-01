package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Ad3bay0c/routex/memory"
	"github.com/Ad3bay0c/routex/tools"
)

func newTestOpenAI(t *testing.T, srv *httptest.Server) *OpenAIAdapter {
	t.Helper()
	a, err := NewOpenAIAdapter(Config{
		APIKey:      "test-key",
		Model:       "gpt-4o",
		BaseURL:     srv.URL,
		MaxTokens:   1024,
		Temperature: 0.7,
		Timeout:     5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewOpenAIAdapter: %v", err)
	}
	return a
}

func openAITextBody(content string) map[string]any {
	return map[string]any{
		"id": "chatcmpl-test", "object": "chat.completion", "model": "gpt-4o",
		"choices": []map[string]any{{
			"index":         0,
			"message":       map[string]any{"role": "assistant", "content": content},
			"finish_reason": "stop",
		}},
		"usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 20},
	}
}

func openAIToolCallBody(calls []map[string]any) map[string]any {
	return map[string]any{
		"id": "chatcmpl-test", "object": "chat.completion", "model": "gpt-4o",
		"choices": []map[string]any{{
			"index": 0,
			"message": map[string]any{
				"role": "assistant", "content": nil, "tool_calls": calls,
			},
			"finish_reason": "tool_calls",
		}},
		"usage": map[string]any{"prompt_tokens": 15, "completion_tokens": 10},
	}
}

func openAITestToolCall(id, name, args string) map[string]any {
	return map[string]any{
		"id": id, "type": "function",
		"function": map[string]any{"name": name, "arguments": args},
	}
}

func TestOpenAI_MissingAPIKey(t *testing.T) {
	_, err := NewOpenAIAdapter(Config{Model: "gpt-4o"})
	if err == nil {
		t.Fatal("should error with empty APIKey")
	}
}

func TestOpenAI_TextResponse(t *testing.T) {
	srv := mockServer(t, http.StatusOK, openAITextBody("Hello from GPT"))
	resp, err := newTestOpenAI(t, srv).Complete(context.Background(), simpleRequest("hello"))
	if err != nil {
		t.Fatalf("Complete() error: %v", err)
	}
	if resp.Content != "Hello from GPT" {
		t.Errorf("Content = %q, want %q", resp.Content, "Hello from GPT")
	}
	if len(resp.ToolCalls) != 0 {
		t.Errorf("ToolCalls should be empty, got %d", len(resp.ToolCalls))
	}
	if resp.Usage.InputTokens != 10 {
		t.Errorf("InputTokens = %d, want 10", resp.Usage.InputTokens)
	}
	if resp.Usage.OutputTokens != 20 {
		t.Errorf("OutputTokens = %d, want 20", resp.Usage.OutputTokens)
	}
	if resp.FinishReason != "stop" {
		t.Errorf("FinishReason = %q, want stop", resp.FinishReason)
	}
}

func TestOpenAI_SingleToolCall(t *testing.T) {
	args := `{"query":"Go 1.26 release notes"}`
	srv := mockServer(t, http.StatusOK, openAIToolCallBody([]map[string]any{
		openAITestToolCall("call_123", "web_search", args),
	}))

	resp, err := newTestOpenAI(t, srv).Complete(context.Background(), Request{
		SystemPrompt: "You are a researcher.",
		History:      simpleHistory("find go 1.26 notes"),
		ToolSchemas:  map[string]tools.Schema{"web_search": {Description: "Search the web"}},
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
	if tc.ToolName != "web_search" {
		t.Errorf("ToolName = %q, want web_search", tc.ToolName)
	}
	if tc.ID != "call_123" {
		t.Errorf("ID = %q, want call_123", tc.ID)
	}
	if tc.Input != args {
		t.Errorf("Input = %q, want %q", tc.Input, args)
	}
}

func TestOpenAI_MultipleToolCalls(t *testing.T) {
	srv := mockServer(t, http.StatusOK, openAIToolCallBody([]map[string]any{
		openAITestToolCall("id1", "web_search", `{"query":"Go"}`),
		openAITestToolCall("id2", "read_file", `{"path":"f.md"}`),
		openAITestToolCall("id3", "wikipedia", `{"topic":"Go"}`),
	}))

	resp, err := newTestOpenAI(t, srv).Complete(context.Background(), simpleRequest("research"))
	if err != nil {
		t.Fatalf("Complete() error: %v", err)
	}
	if len(resp.ToolCalls) != 3 {
		t.Fatalf("ToolCalls len = %d, want 3", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].ToolName != "web_search" {
		t.Errorf("[0].ToolName = %q, want web_search", resp.ToolCalls[0].ToolName)
	}
	if resp.ToolCalls[2].ID != "id3" {
		t.Errorf("[2].ID = %q, want id3", resp.ToolCalls[2].ID)
	}
}

func TestOpenAI_ErrorResponse(t *testing.T) {
	srv := mockServer(t, http.StatusUnauthorized, map[string]any{
		"error": map[string]any{"message": "Invalid API key", "type": "invalid_request_error"},
	})
	_, err := newTestOpenAI(t, srv).Complete(context.Background(), simpleRequest("hello"))
	if err == nil {
		t.Fatal("Complete() should error for 401")
	}
}

func TestOpenAI_EmptyChoices(t *testing.T) {
	srv := mockServer(t, http.StatusOK, map[string]any{
		"id": "test", "object": "chat.completion", "model": "gpt-4o",
		"choices": []map[string]any{},
		"usage":   map[string]any{"prompt_tokens": 5, "completion_tokens": 0},
	})
	resp, err := newTestOpenAI(t, srv).Complete(context.Background(), simpleRequest("hello"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.FinishReason != "no_choices" {
		t.Errorf("FinishReason = %q, want no_choices", resp.FinishReason)
	}
}

func TestOpenAI_RequestContainsSystemPrompt(t *testing.T) {
	var captured []byte
	srv := mockServerFunc(t, func(w http.ResponseWriter, r *http.Request) {
		captured, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(openAITextBody("ok"))
	})

	prompt := "You are a weather reporter."
	newTestOpenAI(t, srv).Complete(context.Background(), Request{
		SystemPrompt: prompt,
		History:      simpleHistory("weather?"),
	})

	var body map[string]any
	json.Unmarshal(captured, &body)
	messages := body["messages"].([]any)
	first := messages[0].(map[string]any)
	if first["role"] != "system" {
		t.Errorf("first message role = %q, want system", first["role"])
	}
	if first["content"] != prompt {
		t.Errorf("system content = %q, want %q", first["content"], prompt)
	}
}

func TestOpenAI_AuthHeader(t *testing.T) {
	var capturedAuth string
	srv := mockServerFunc(t, func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(openAITextBody("ok"))
	})
	newTestOpenAI(t, srv).Complete(context.Background(), simpleRequest("hello"))
	if capturedAuth != "Bearer test-key" {
		t.Errorf("Authorization = %q, want Bearer test-key", capturedAuth)
	}
}

func TestOpenAI_ContextCancellation(t *testing.T) {
	ctx := context.Background()
	srv := mockServerFunc(t, func(w http.ResponseWriter, r *http.Request) {
		<-ctx.Done()
	})
	ctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	_, err := newTestOpenAI(t, srv).Complete(ctx, simpleRequest("hello"))
	if err == nil {
		t.Fatal("should error when context cancelled")
	}
}

func TestOpenAI_ModelAndProvider(t *testing.T) {
	a := &OpenAIAdapter{model: "gpt-4o"}
	if a.Model() != "gpt-4o" {
		t.Errorf("Model() = %q, want gpt-4o", a.Model())
	}
	if a.Provider() != "openai" {
		t.Errorf("Provider() = %q, want openai", a.Provider())
	}
}

func TestBuildOpenAIMessages_SystemPromptFirst(t *testing.T) {
	msgs := buildOpenAIMessages("be helpful", simpleHistory("hello"))
	if msgs[0].Role != "system" {
		t.Errorf("first role = %q, want system", msgs[0].Role)
	}
	if msgs[0].Content != "be helpful" {
		t.Errorf("system content = %q, want be helpful", msgs[0].Content)
	}
}

func TestBuildOpenAIMessages_EmptySystemPrompt(t *testing.T) {
	msgs := buildOpenAIMessages("", simpleHistory("hello"))
	if msgs[0].Role == "system" {
		t.Error("should not add system message when empty")
	}
}

func TestTranslateOpenAIResponse_NoChoices(t *testing.T) {
	resp := translateOpenAIResponse(openAIResponse{})
	if resp.FinishReason != "no_choices" {
		t.Errorf("FinishReason = %q, want no_choices", resp.FinishReason)
	}
}

func TestBuildOpenAIMessages_UserAssistantAndTools(t *testing.T) {
	hist := []memory.Message{
		{Role: "user", Content: "task"},
		{Role: "assistant", ToolCalls: []memory.ToolCallRecord{
			{ID: "c1", ToolName: "a", Input: `{"x":1}`},
			{ID: "c2", ToolName: "b", Input: `{"y":2}`},
		}},
		{Role: "user", ToolCall: &memory.ToolCallRecord{ID: "c1", Output: `"out1"`}},
		{Role: "assistant", ToolCall: &memory.ToolCallRecord{ID: "c3", ToolName: "grep", Input: `{}`}},
		{Role: "user", ToolCall: &memory.ToolCallRecord{ID: "c3", Output: `"done"`, Error: "tool failed"}},
		{Role: "assistant", Content: "final"},
	}
	msgs := buildOpenAIMessages("be helpful", hist)
	if len(msgs) != 7 {
		t.Fatalf("msgs len = %d, want 7: %+v", len(msgs), msgs)
	}
	if msgs[0].Role != "system" || msgs[0].Content != "be helpful" {
		t.Errorf("system: %+v", msgs[0])
	}
	if msgs[1].Role != "user" || msgs[1].Content != "task" {
		t.Errorf("user: %+v", msgs[1])
	}
	if msgs[2].Role != "assistant" || len(msgs[2].ToolCalls) != 2 {
		t.Errorf("assistant multi: %+v", msgs[2])
	}
	if msgs[3].Role != "tool" || msgs[3].ToolCallID != "c1" {
		t.Errorf("tool result: %+v", msgs[3])
	}
	if msgs[4].Role != "assistant" || len(msgs[4].ToolCalls) != 1 || msgs[4].ToolCalls[0].Function.Name != "grep" {
		t.Errorf("assistant single tool: %+v", msgs[4])
	}
	if msgs[5].Role != "tool" || msgs[5].ToolCallID != "c3" {
		t.Errorf("tool result 2: %+v", msgs[5])
	}
	if msgs[6].Role != "assistant" || msgs[6].Content != "final" {
		t.Errorf("final assistant: %+v", msgs[6])
	}
}

func TestBuildOpenAIMessages_EmptySystem(t *testing.T) {
	msgs := buildOpenAIMessages("", []memory.Message{{Role: "user", Content: "hi"}})
	if len(msgs) != 1 || msgs[0].Role != "user" {
		t.Fatalf("%+v", msgs)
	}
}

func TestBuildOpenAITools_RequiredField(t *testing.T) {
	out := buildOpenAITools(map[string]tools.Schema{
		"t": {
			Description: "d",
			Parameters: map[string]tools.Parameter{
				"q": {Type: "string", Description: "query", Required: true},
				"o": {Type: "string", Description: "opt", Required: false},
			},
		},
	})
	if len(out) != 1 {
		t.Fatalf("len = %d", len(out))
	}
	var params map[string]any
	if err := json.Unmarshal(out[0].Function.Parameters, &params); err != nil {
		t.Fatal(err)
	}
	req, _ := params["required"].([]any)
	if len(req) != 1 || req[0] != "q" {
		t.Errorf("required = %v", params["required"])
	}
}
