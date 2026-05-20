package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Ad3bay0c/routex/tools"
)

func newTestGemini(t *testing.T, srv *httptest.Server) *GeminiAdapter {
	t.Helper()
	a, err := NewGeminiAdapter(Config{
		APIKey:      "test-key",
		Model:       "gemini-2.0-flash",
		BaseURL:     srv.URL,
		MaxTokens:   1024,
		Temperature: 0.7,
		Timeout:     5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewGeminiAdapter: %v", err)
	}
	return a
}

func geminiTextBody(text string) map[string]any {
	return map[string]any{
		"candidates": []map[string]any{{
			"content": map[string]any{
				"role":  "model",
				"parts": []map[string]any{{"text": text}},
			},
			"finishReason": "STOP",
		}},
		"usageMetadata": map[string]any{
			"promptTokenCount":     12,
			"candidatesTokenCount": 25,
		},
	}
}

func geminiToolCallBody(parts []map[string]any) map[string]any {
	return map[string]any{
		"candidates": []map[string]any{{
			"content": map[string]any{
				"role":  "model",
				"parts": parts,
			},
			"finishReason": "STOP",
		}},
		"usageMetadata": map[string]any{
			"promptTokenCount":     20,
			"candidatesTokenCount": 15,
		},
	}
}

func geminiFunctionCall(id, name string, args map[string]any) map[string]any {
	fc := map[string]any{"name": name, "args": args}
	if id != "" {
		fc["id"] = id
	}
	return map[string]any{"functionCall": fc}
}

func TestGemini_MissingAPIKey(t *testing.T) {
	_, err := NewGeminiAdapter(Config{Model: "gemini-2.0-flash"})
	if err == nil {
		t.Fatal("should error with empty APIKey")
	}
}

func TestGemini_MissingModel(t *testing.T) {
	_, err := NewGeminiAdapter(Config{APIKey: "k"})
	if err == nil {
		t.Fatal("should error with empty Model")
	}
}

func TestGemini_TextResponse(t *testing.T) {
	srv := mockServer(t, http.StatusOK, geminiTextBody("Hello from Gemini"))
	resp, err := newTestGemini(t, srv).Complete(context.Background(), simpleRequest("hello"))
	if err != nil {
		t.Fatalf("Complete() error: %v", err)
	}
	if resp.Content != "Hello from Gemini" {
		t.Errorf("Content = %q, want Hello from Gemini", resp.Content)
	}
	if len(resp.ToolCalls) != 0 {
		t.Errorf("ToolCalls should be empty, got %d", len(resp.ToolCalls))
	}
	if resp.Usage.InputTokens != 12 {
		t.Errorf("InputTokens = %d, want 12", resp.Usage.InputTokens)
	}
	if resp.FinishReason != "STOP" {
		t.Errorf("FinishReason = %q, want STOP", resp.FinishReason)
	}
}

func TestGemini_SingleToolCall(t *testing.T) {
	srv := mockServer(t, http.StatusOK, geminiToolCallBody([]map[string]any{
		geminiFunctionCall("call_abc", "web_search", map[string]any{"query": "Lagos weather"}),
	}))

	resp, err := newTestGemini(t, srv).Complete(context.Background(), Request{
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
	if tc.ID != "call_abc" {
		t.Errorf("ID = %q, want call_abc", tc.ID)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(tc.Input), &parsed); err != nil {
		t.Fatalf("Input not valid JSON: %v", err)
	}
	if parsed["query"] != "Lagos weather" {
		t.Errorf("query = %v", parsed["query"])
	}
}

func TestGemini_ToolCallWithoutID(t *testing.T) {
	srv := mockServer(t, http.StatusOK, geminiToolCallBody([]map[string]any{
		geminiFunctionCall("", "grep", map[string]any{"pattern": "foo"}),
	}))

	resp, err := newTestGemini(t, srv).Complete(context.Background(), simpleRequest("find foo"))
	if err != nil {
		t.Fatalf("Complete() error: %v", err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("ToolCalls len = %d, want 1", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].ID == "" {
		t.Error("expected generated tool call ID")
	}
	if !strings.HasPrefix(resp.ToolCalls[0].ID, "call_") {
		t.Errorf("ID = %q, want call_ prefix", resp.ToolCalls[0].ID)
	}
}

func TestGemini_MultipleToolCalls(t *testing.T) {
	srv := mockServer(t, http.StatusOK, geminiToolCallBody([]map[string]any{
		geminiFunctionCall("c1", "web_search", map[string]any{"query": "Go"}),
		geminiFunctionCall("c2", "read_file", map[string]any{"path": "f.md"}),
	}))

	resp, err := newTestGemini(t, srv).Complete(context.Background(), simpleRequest("research"))
	if err != nil {
		t.Fatalf("Complete() error: %v", err)
	}
	if len(resp.ToolCalls) != 2 {
		t.Fatalf("ToolCalls len = %d, want 2", len(resp.ToolCalls))
	}
}

func TestGemini_APIErrorInBody(t *testing.T) {
	srv := mockServer(t, http.StatusOK, map[string]any{
		"error": map[string]any{
			"code": 429, "status": "RESOURCE_EXHAUSTED", "message": "quota exceeded",
		},
	})
	_, err := newTestGemini(t, srv).Complete(context.Background(), simpleRequest("hello"))
	if err == nil {
		t.Fatal("expected error from API error object")
	}
	if !strings.Contains(err.Error(), "gemini") || !strings.Contains(err.Error(), "quota") {
		t.Errorf("error = %v", err)
	}
}

func TestGemini_RequestHeaders(t *testing.T) {
	var apiKey, contentType string
	srv := mockServerFunc(t, func(w http.ResponseWriter, r *http.Request) {
		apiKey = r.Header.Get("x-goog-api-key")
		contentType = r.Header.Get("Content-Type")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(geminiTextBody("ok"))
	})

	newTestGemini(t, srv).Complete(context.Background(), simpleRequest("hello"))

	if apiKey != "test-key" {
		t.Errorf("x-goog-api-key = %q, want test-key", apiKey)
	}
	if contentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", contentType)
	}
}

func TestGemini_ModelAndProvider(t *testing.T) {
	a := &GeminiAdapter{model: "gemini-2.0-flash"}
	if a.Model() != "gemini-2.0-flash" {
		t.Errorf("Model() = %q, want gemini-2.0-flash", a.Model())
	}
	if a.Provider() != "gemini" {
		t.Errorf("Provider() = %q, want gemini", a.Provider())
	}
}

func TestGemini_DefaultEndpointIncludesModel(t *testing.T) {
	a, err := NewGeminiAdapter(Config{
		APIKey: "k",
		Model:  "gemini-2.5-pro",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-pro:generateContent"
	if a.endpoint != want {
		t.Errorf("endpoint = %q, want %q", a.endpoint, want)
	}
}

func TestNew_GeminiProvider(t *testing.T) {
	adapter, err := New(Config{
		Provider: "gemini",
		Model:    "gemini-2.0-flash",
		APIKey:   "test-key",
		BaseURL:  "http://localhost:9999",
		Timeout:  time.Second,
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	if adapter.Provider() != "gemini" {
		t.Errorf("Provider() = %q, want gemini", adapter.Provider())
	}
}
