package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func anthropicResponse(text string) map[string]any {
	return map[string]any{
		"id":   "msg_test",
		"type": "message",
		"content": []map[string]any{
			{"type": "text", "text": text},
		},
		"usage": map[string]any{"input_tokens": 10, "output_tokens": 20},
	}
}

func TestSummarise_ParagraphStyle(t *testing.T) {
	summary := "Go is a compiled language designed at Google for simplicity and performance."

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "messages") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(anthropicResponse(summary))
	}))
	t.Cleanup(srv.Close)

	tool := &SummariseTool{
		client:           srv.Client(),
		apiKey:           "test-key",
		model:            "claude-haiku-test",
		anthropicBaseURL: srv.URL,
	}

	result, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"text":  "Go is a statically typed, compiled programming language designed at Google by Robert Griesemer, Rob Pike, and Ken Thompson.",
		"style": "paragraph",
	}))
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	var out summariseOutput
	mustUnmarshal(t, result, &out)

	if out.Summary != summary {
		t.Errorf("Summary = %q, want %q", out.Summary, summary)
	}
	if out.Style != "paragraph" {
		t.Errorf("Style = %q, want %q", out.Style, "paragraph")
	}
	if out.WordCount != countWords(summary) {
		t.Error("WordCount should be non-zero")
	}
}

func TestSummarise_BulletStyle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Capture request body and verify style is included in prompt
		var reqBody map[string]any
		json.NewDecoder(r.Body).Decode(&reqBody)
		messages := reqBody["messages"].([]any)
		userMsg := messages[0].(map[string]any)
		if !strings.Contains(userMsg["content"].(string), "bullet") {
			t.Error("prompt should mention bullet for bullet_points style")
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(anthropicResponse("• Point one\n• Point two"))
	}))
	t.Cleanup(srv.Close)

	tool := &SummariseTool{
		client:           srv.Client(),
		apiKey:           "test-key",
		model:            "test",
		anthropicBaseURL: srv.URL,
	}

	result, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"text":  "Some long text to summarise into bullets.",
		"style": "bullet_points",
	}))
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	var out summariseOutput
	mustUnmarshal(t, result, &out)
	if out.Style != "bullet_points" {
		t.Errorf("Style = %q, want %q", out.Style, "bullet_points")
	}
}

func TestSummarise_InvalidAPIKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)

	tool := &SummariseTool{
		client:           srv.Client(),
		apiKey:           "bad-key",
		model:            "test",
		anthropicBaseURL: srv.URL,
	}

	_, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"text": "some text",
	}))
	if err == nil {
		t.Error("should error for 401 response")
	}
}

func TestSummarise_EmptyText(t *testing.T) {
	tool := Summarise("key")
	_, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{"text": ""}))
	if err == nil {
		t.Error("should error for empty text")
	}
}

func TestSummarise_DefaultStyle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(anthropicResponse("summary"))
	}))
	t.Cleanup(srv.Close)

	tool := &SummariseTool{client: srv.Client(), apiKey: "k", model: "m", anthropicBaseURL: srv.URL}
	result, _ := tool.Execute(context.Background(), mustMarshal(t, map[string]any{"text": "text"}))

	var out summariseOutput
	mustUnmarshal(t, result, &out)
	if out.Style != "paragraph" {
		t.Errorf("default Style = %q, want %q", out.Style, "paragraph")
	}
}

func TestSummarise_NameAndSchema(t *testing.T) {
	tool := Summarise("key")
	if tool.Name() != "summarise" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "summarise")
	}
	if tool.Schema().Description == "" {
		t.Error("Schema.Description should not be empty")
	}
}
