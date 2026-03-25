package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
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

// openAIToolCallResponse returns an OpenAI response requesting a tool call.
func openAIToolCallResponse(toolName, argsJSON string) map[string]any {
	return map[string]any{
		"id":     "chatcmpl-test",
		"object": "chat.completion",
		"model":  "gpt-4o",
		"choices": []map[string]any{
			{
				"index": 0,
				"message": map[string]any{
					"role":    "assistant",
					"content": nil,
					"tool_calls": []map[string]any{
						{
							"id":   "call_test_123",
							"type": "function",
							"function": map[string]any{
								"name":      toolName,
								"arguments": argsJSON,
							},
						},
					},
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

func TestOpenAIAdapter_TextResponse(t *testing.T) {
	content := "Hello from mock GPT"
	srv := mockServer(t, http.StatusOK, openAITextResponse(content))

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

	if resp.Content != content {
		t.Errorf("got = %q, want %q", resp.Content, content)
	}
	if resp.ToolCall != nil {
		t.Errorf("ToolCall should be nil for text response")
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

func TestOpenAIAdapter_ToolCallResponse(t *testing.T) {
	argsJSON := `{"query":"Go 1.26 release notes"}`
	srv := mockServer(t, http.StatusOK, openAIToolCallResponse("web_search", argsJSON))

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
			"web_search": {
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
		t.Errorf("Content should be empty for tool call response, got %q", resp.Content)
	}
	if resp.ToolCall == nil {
		t.Fatalf("ToolCall should not be nil")
	}
	if resp.ToolCall.ToolName != "web_search" {
		t.Errorf("ToolName = %q, want %q", resp.ToolCall.ToolName, "web_search")
	}
	if resp.ToolCall.ID != "call_test_123" {
		t.Errorf("ToolCall.ID = %q, want %q", resp.ToolCall.ID, "call_test_123")
	}
	if resp.ToolCall.Input != argsJSON {
		t.Errorf("ToolCall.Input = %q, want %q", resp.ToolCall.Input, argsJSON)
	}
	if resp.FinishReason != "tool_calls" {
		t.Errorf("FinishReason = %q, want %q", resp.FinishReason, "tool_calls")
	}
}

func TestOpenAIAdapter_ErrorResponse(t *testing.T) {
	srv := mockServer(t, http.StatusUnauthorized, map[string]any{
		"error": map[string]any{
			"message": "Invalid API key",
			"type":    "invalid_request_error",
		},
	})

	adapter := &OpenAIAdapter{
		client:      newOpenAIClientWithBaseURL(t, srv.URL),
		model:       "gpt-4o",
		maxTokens:   1024,
		temperature: 0.7,
		timeout:     5 * time.Second,
	}

	_, err := adapter.Complete(context.Background(), simpleRequest("hello"))
	if err == nil {
		t.Fatal("Complete() should return error for 401 response")
	}
	if !strings.Contains(err.Error(), "openai: api call failed") {
		t.Errorf("error = %q, want 'openai: api call failed'", err)
	}
}

func TestOpenAIAdapter_EmptyChoices(t *testing.T) {
	srv := mockServer(t, http.StatusOK, map[string]any{
		"id":      "chatcmpl-test",
		"object":  "chat.completion",
		"model":   "gpt-4o",
		"choices": []map[string]any{},
		"usage":   map[string]any{"prompt_tokens": 5, "completion_tokens": 0, "total_tokens": 5},
	})

	adapter := &OpenAIAdapter{
		client:      newOpenAIClientWithBaseURL(t, srv.URL),
		model:       "gpt-4o",
		maxTokens:   1024,
		temperature: 0.7,
		timeout:     5 * time.Second,
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
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(openAITextResponse("ok"))
	})

	adapter := &OpenAIAdapter{
		client:      newOpenAIClientWithBaseURL(t, srv.URL),
		model:       "gpt-4o",
		maxTokens:   1024,
		temperature: 0.7,
		timeout:     5 * time.Second,
	}

	systemPrompt := "You are a weather reporter."
	_, err := adapter.Complete(context.Background(), Request{
		SystemPrompt: systemPrompt,
		History:      simpleHistory("what is the weather?"),
	})
	if err != nil {
		t.Fatalf("Complete() error: %v", err)
	}

	// The captured request body should contain the system prompt
	// as the first message with role "system"
	var body map[string]any
	if err := json.Unmarshal(capturedBody, &body); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}

	messages, ok := body["messages"].([]any)
	if !ok || len(messages) == 0 {
		t.Fatal("request body missing messages array")
	}

	firstMsg, ok := messages[0].(map[string]any)
	if !ok {
		t.Fatal("first message is not an object")
	}
	if firstMsg["role"] != "system" {
		t.Errorf("first message role = %q, want %q", firstMsg["role"], "system")
	}
	if firstMsg["content"] != systemPrompt {
		t.Errorf("first message content = %q, want %q", firstMsg["content"], systemPrompt)
	}
}

func TestOpenAIAdapter_ModelAndProvider(t *testing.T) {
	adapter := &OpenAIAdapter{model: "gpt-4o"}
	if adapter.Model() != "gpt-4o" {
		t.Errorf("Model() = %q, want %q", adapter.Model(), "gpt-4o")
	}
	if adapter.Provider() != "openai" {
		t.Errorf("Provider() = %q, want %q", adapter.Provider(), "openai")
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
	// Server that hangs until context is cancelled
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
