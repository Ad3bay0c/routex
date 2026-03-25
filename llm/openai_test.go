package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/sashabaranov/go-openai"

	"github.com/Ad3bay0c/routex/tools"
)

func newOpenAIClientWithBaseURL(t *testing.T, baseURL string) *openai.Client {
	t.Helper()
	cfg := openai.DefaultConfig("test-key")
	cfg.BaseURL = baseURL + "/v1"
	return openai.NewClientWithConfig(cfg)
}

func openaiChatCompletionResponse(choices []openai.ChatCompletionChoice) openai.ChatCompletionResponse {
	if choices == nil {
		choices = []openai.ChatCompletionChoice{}
	}
	return openai.ChatCompletionResponse{
		ID:      "chat-completion-test",
		Object:  "chat.completion",
		Model:   "gpt-4o",
		Choices: choices,
		Usage: openai.Usage{
			PromptTokens:     5,
			CompletionTokens: 0,
			TotalTokens:      5,
		},
	}
}

// openAITextResponse returns a minimal OpenAI chat completion JSON body
// with a plain text response.
func openAITextResponse(content string) map[string]any {
	return map[string]any{
		"id":     "chatcmpl-test",
		"object": "chat.completion",
		"model":  "gpt-4o",
		"choices": []map[string]any{
			{
				"index": 0,
				"message": map[string]any{
					"role":    "assistant",
					"content": content,
				},
				"finish_reason": "stop",
			},
		},
		"usage": map[string]any{
			"prompt_tokens":     10,
			"completion_tokens": 20,
			"total_tokens":      30,
		},
	}
}

func openAIToolCallResponse(calls []map[string]any) map[string]any {
	return map[string]any{
		"id":     "chatcmpl-test",
		"object": "chat.completion",
		"model":  "gpt-4o",
		"choices": []map[string]any{
			{
				"index": 0,
				"message": map[string]any{
					"role":       "assistant",
					"content":    nil,
					"tool_calls": calls,
				},
				"finish_reason": "tool_calls",
			},
		},
		"usage": map[string]any{
			"prompt_tokens":     15,
			"completion_tokens": 10,
			"total_tokens":      25,
		},
	}
}

func openAISingleToolCall(id, name, args string) map[string]any {
	return map[string]any{
		"id":   id,
		"type": "function",
		"function": map[string]any{
			"name":      name,
			"arguments": args,
		},
	}
}

func TestOpenAIAdapter_TextResponse(t *testing.T) {
	srv := mockServer(t, http.StatusOK, openAITextResponse("Hello from mock GPT"))

	adapter := &OpenAIAdapter{
		client:      newOpenAIClientWithBaseURL(t, srv.URL),
		model:       "gpt-4o",
		maxTokens:   1024,
		temperature: 0.7,
		timeout:     5 * time.Second,
	}

	resp, err := adapter.Complete(context.Background(), simpleRequest("say hello"))
	if err != nil {
		t.Fatalf("Complete() error: %v", err)
	}

	if resp.Content != "Hello from mock GPT" {
		t.Errorf("Content = %q, want %q", resp.Content, "Hello from mock GPT")
	}
	if len(resp.ToolCalls) != 0 {
		t.Errorf("ToolCalls should be empty for text response, got %d", len(resp.ToolCalls))
	}
	if resp.Usage.InputTokens != 10 {
		t.Errorf("InputTokens = %d, want 10", resp.Usage.InputTokens)
	}
	if resp.Usage.OutputTokens != 20 {
		t.Errorf("OutputTokens = %d, want 20", resp.Usage.OutputTokens)
	}
	if resp.Usage.Total() != 30 {
		t.Errorf("Total() = %d, want 30", resp.Usage.Total())
	}
	if resp.FinishReason != "stop" {
		t.Errorf("FinishReason = %q, want %q", resp.FinishReason, "stop")
	}
}

func TestOpenAIAdapter_SingleToolCall(t *testing.T) {
	argsJSON := `{"query":"Go 1.24 release notes"}`
	srv := mockServer(t, http.StatusOK, openAIToolCallResponse([]map[string]any{
		openAISingleToolCall("call_test_123", "web_search", argsJSON),
	}))

	adapter := &OpenAIAdapter{
		client:      newOpenAIClientWithBaseURL(t, srv.URL),
		model:       "gpt-4o",
		maxTokens:   1024,
		temperature: 0.7,
		timeout:     5 * time.Second,
	}

	resp, err := adapter.Complete(context.Background(), Request{
		SystemPrompt: "You are a researcher.",
		History:      simpleHistory("find go 1.24 notes"),
		ToolSchemas: map[string]tools.Schema{
			"web_search": {Description: "Search the web"},
		},
	})
	if err != nil {
		t.Fatalf("Complete() error: %v", err)
	}

	if resp.Content != "" {
		t.Errorf("Content should be empty for tool call response, got %q", resp.Content)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("ToolCalls len = %d, want 1", len(resp.ToolCalls))
	}
	tc := resp.ToolCalls[0]
	if tc.ToolName != "web_search" {
		t.Errorf("ToolName = %q, want %q", tc.ToolName, "web_search")
	}
	if tc.ID != "call_test_123" {
		t.Errorf("ID = %q, want %q", tc.ID, "call_test_123")
	}
	if tc.Input != argsJSON {
		t.Errorf("Input = %q, want %q", tc.Input, argsJSON)
	}
	if resp.FinishReason != "tool_calls" {
		t.Errorf("FinishReason = %q, want %q", resp.FinishReason, "tool_calls")
	}
}

func TestOpenAIAdapter_MultipleToolCalls(t *testing.T) {
	// The LLM requests three independent tools in one response
	srv := mockServer(t, http.StatusOK, openAIToolCallResponse([]map[string]any{
		openAISingleToolCall("call_1", "web_search", `{"query":"Go 1.24"}`),
		openAISingleToolCall("call_2", "read_file", `{"path":"notes.md"}`),
		openAISingleToolCall("call_3", "wikipedia", `{"topic":"Go language"}`),
	}))

	adapter := &OpenAIAdapter{
		client: newOpenAIClientWithBaseURL(t, srv.URL),
		model:  "gpt-4o", maxTokens: 1024, temperature: 0.7, timeout: 5 * time.Second,
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
	// Each ID must be preserved correctly for matching results back
	if resp.ToolCalls[0].ID != "call_1" {
		t.Errorf("[0].ID = %q, want call_1", resp.ToolCalls[0].ID)
	}
	if resp.ToolCalls[2].ID != "call_3" {
		t.Errorf("[2].ID = %q, want call_3", resp.ToolCalls[2].ID)
	}
	if resp.Content != "" {
		t.Errorf("Content should be empty when tool calls present")
	}
}

func TestOpenAIAdapter_ErrorResponse(t *testing.T) {
	srv := mockServer(t, http.StatusUnauthorized, map[string]any{
		"error": map[string]any{"message": "Invalid API key", "type": "invalid_request_error"},
	})

	adapter := &OpenAIAdapter{
		client: newOpenAIClientWithBaseURL(t, srv.URL),
		model:  "gpt-4o", maxTokens: 1024, temperature: 0.7, timeout: 5 * time.Second,
	}
	_, err := adapter.Complete(context.Background(), simpleRequest("hello"))
	if err == nil {
		t.Fatal("Complete() should return error for 401 response")
	}
}

func TestOpenAIAdapter_EmptyChoices(t *testing.T) {
	srv := mockServer(t, http.StatusOK, map[string]any{
		"id": "chatcmpl-test", "object": "chat.completion", "model": "gpt-4o",
		"choices": []map[string]any{},
		"usage":   map[string]any{"prompt_tokens": 5, "completion_tokens": 0, "total_tokens": 5},
	})

	adapter := &OpenAIAdapter{
		client: newOpenAIClientWithBaseURL(t, srv.URL),
		model:  "gpt-4o", maxTokens: 1024, temperature: 0.7, timeout: 5 * time.Second,
	}
	resp, err := adapter.Complete(context.Background(), simpleRequest("hello"))
	if err != nil {
		t.Fatalf("Complete() unexpected error: %v", err)
	}
	if resp.FinishReason != "no_choices" {
		t.Errorf("FinishReason = %q, want %q", resp.FinishReason, "no_choices")
	}
}

func TestOpenAIAdapter_RequestContainsSystemPrompt(t *testing.T) {
	var capturedBody []byte

	srv := mockServerFunc(t, func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = readAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(openAITextResponse("ok"))
	})

	adapter := &OpenAIAdapter{
		client: newOpenAIClientWithBaseURL(t, srv.URL),
		model:  "gpt-4o", maxTokens: 1024, temperature: 0.7, timeout: 5 * time.Second,
	}

	systemPrompt := "You are a weather reporter."
	_, err := adapter.Complete(context.Background(), Request{
		SystemPrompt: systemPrompt,
		History:      simpleHistory("what is the weather?"),
	})
	if err != nil {
		t.Fatalf("Complete() error: %v", err)
	}

	var body map[string]any
	if err := json.Unmarshal(capturedBody, &body); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	messages := body["messages"].([]any)
	if len(messages) == 0 {
		t.Fatal("request body missing messages array")
	}
	firstMsg := messages[0].(map[string]any)
	if firstMsg["role"] != "system" {
		t.Errorf("first message role = %q, want system", firstMsg["role"])
	}
	if firstMsg["content"] != systemPrompt {
		t.Errorf("first message content = %q, want %q", firstMsg["content"], systemPrompt)
	}
}

func TestOpenAIAdapter_ModelAndProvider(t *testing.T) {
	adapter := &OpenAIAdapter{model: "gpt-4o"}
	if adapter.Model() != "gpt-4o" {
		t.Errorf("Model() = %q, want gpt-4o", adapter.Model())
	}
	if adapter.Provider() != "openai" {
		t.Errorf("Provider() = %q, want openai", adapter.Provider())
	}
}

func TestOpenAIAdapter_MissingAPIKey(t *testing.T) {
	_, err := NewOpenAIAdapter(Config{Model: "gpt-4o"})
	if err == nil {
		t.Fatal("NewOpenAIAdapter() should error with empty APIKey")
	}
	if err.Error() != "openai: api_key is required" {
		t.Errorf("Error() = %q, want %q", err, "openai: api_key is required")
	}
}

func TestOpenAIAdapter_ContextCancellation(t *testing.T) {
	ctx := context.Background()
	srv := mockServerFunc(t, func(w http.ResponseWriter, r *http.Request) {
		<-ctx.Done()
	})

	adapter := &OpenAIAdapter{
		client:      newOpenAIClientWithBaseURL(t, srv.URL),
		model:       "gpt-4o",
		maxTokens:   1024,
		temperature: 0.7,
		timeout:     5 * time.Second,
	}

	ctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()

	_, err := adapter.Complete(ctx, simpleRequest("hello"))
	if err == nil {
		t.Fatal("Complete() should return error when context is cancelled")
	}
}

func TestBuildOpenAIMessages_SystemPromptFirst(t *testing.T) {
	messages := buildOpenAIMessages("be helpful", simpleHistory("hello"))
	if len(messages) != 2 {
		t.Fatalf("expected at least 2 messages, got %d", len(messages))
	}
	if messages[0].Role != "system" {
		t.Errorf("first message role = %q, want %q", messages[0].Role, "system")
	}
	if messages[0].Content != "be helpful" {
		t.Errorf("system message content = %q, want %q", messages[0].Content, "be helpful")
	}
	if messages[1].Role != "user" {
		t.Errorf("second message role = %q, want %q", messages[1].Role, "user")
	}
}

func TestBuildOpenAIMessages_EmptySystemPrompt(t *testing.T) {
	messages := buildOpenAIMessages("", simpleHistory("hello"))
	if len(messages) != 1 {
		t.Errorf("messages length = %d, want %d", len(messages), 1)
	}
	if messages[0].Role == "system" {
		t.Error("should not add system message when SystemPrompt is empty")
	}
}

func TestTranslateOpenAIResponse_NoChoices(t *testing.T) {
	openAICompletionResponse := openaiChatCompletionResponse(nil)
	resp := translateOpenAIResponse(openAICompletionResponse)
	if resp.FinishReason != "no_choices" {
		t.Errorf("FinishReason = %q, want %q", resp.FinishReason, "no_choices")
	}
}
