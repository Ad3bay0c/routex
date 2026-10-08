package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Ad3bay0c/routex/memory"
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

func TestGemini_CustomAPIRootBuildsEndpoint(t *testing.T) {
	a, err := NewGeminiAdapter(Config{
		APIKey:  "k",
		Model:   "gemini-2.0-flash",
		BaseURL: "https://custom.example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "https://custom.example.com/v1beta/models/gemini-2.0-flash:generateContent"
	if a.endpoint != want {
		t.Errorf("endpoint = %q, want %q", a.endpoint, want)
	}
}

func TestGemini_HTTPErrorStatus(t *testing.T) {
	srv := mockServer(t, http.StatusBadRequest, map[string]any{"message": "bad request"})
	_, err := newTestGemini(t, srv).Complete(context.Background(), simpleRequest("hello"))
	if err == nil {
		t.Fatal("expected HTTP error")
	}
	if !strings.Contains(err.Error(), "HTTP 400") {
		t.Errorf("error = %v", err)
	}
}

func TestGemini_InvalidJSONResponse(t *testing.T) {
	srv := mockServerFunc(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "not-json")
	})
	_, err := newTestGemini(t, srv).Complete(context.Background(), simpleRequest("hello"))
	if err == nil {
		t.Fatal("expected decode error")
	}
	if !strings.Contains(err.Error(), "decode") {
		t.Errorf("error = %v", err)
	}
}

func TestGemini_NoCandidates(t *testing.T) {
	srv := mockServer(t, http.StatusOK, map[string]any{
		"candidates":    []any{},
		"usageMetadata": map[string]any{},
	})
	resp, err := newTestGemini(t, srv).Complete(context.Background(), simpleRequest("hello"))
	if err != nil {
		t.Fatalf("Complete() error: %v", err)
	}
	if resp.FinishReason != "no_candidates" {
		t.Errorf("FinishReason = %q, want no_candidates", resp.FinishReason)
	}
}

func TestGemini_ToolCallEmptyArgs(t *testing.T) {
	srv := mockServer(t, http.StatusOK, geminiToolCallBody([]map[string]any{
		geminiFunctionCall("id1", "noop", nil),
	}))
	resp, err := newTestGemini(t, srv).Complete(context.Background(), simpleRequest("x"))
	if err != nil {
		t.Fatal(err)
	}
	if resp.ToolCalls[0].Input != "{}" {
		t.Errorf("Input = %q, want {}", resp.ToolCalls[0].Input)
	}
}

func TestGeminiError_ErrorString(t *testing.T) {
	err := (&geminiError{Status: "INVALID", Message: "bad"}).Error()
	if !strings.Contains(err, "gemini") || !strings.Contains(err, "INVALID") {
		t.Errorf("Error() = %q", err)
	}
}

func TestBuildGeminiContents_UserAssistantAndTools(t *testing.T) {
	hist := []memory.Message{
		{Role: "user", Content: "task"},
		{Role: "assistant", ToolCalls: []memory.ToolCallRecord{
			{ID: "c1", ToolName: "a", Input: `{"x":1}`},
			{ID: "c2", ToolName: "b", Input: `{"y":2}`},
		}},
		{Role: "user", ToolCall: &memory.ToolCallRecord{ID: "c1", ToolName: "a", Output: `{"ok":true}`}},
		{Role: "assistant", ToolCall: &memory.ToolCallRecord{ID: "c3", ToolName: "grep", Input: `{}`}},
		{Role: "user", ToolCall: &memory.ToolCallRecord{ID: "c3", ToolName: "grep", Output: "fail", Error: "tool failed"}},
		{Role: "assistant", Content: "final"},
	}
	contents := buildGeminiContents(hist)
	if len(contents) != 6 {
		t.Fatalf("len = %d, want 6: %+v", len(contents), contents)
	}
	if contents[0].Role != "user" || contents[0].Parts[0].Text != "task" {
		t.Errorf("user text: %+v", contents[0])
	}
	if contents[1].Role != "model" || len(contents[1].Parts) != 2 {
		t.Errorf("model multi tool: %+v", contents[1])
	}
	if contents[1].Parts[0].FunctionCall == nil || contents[1].Parts[0].FunctionCall.ID != "c1" {
		t.Errorf("first call: %+v", contents[1].Parts[0])
	}
	if contents[2].Parts[0].FunctionResponse == nil || contents[2].Parts[0].FunctionResponse.Name != "a" {
		t.Errorf("tool result ok: %+v", contents[2].Parts[0])
	}
	if _, ok := contents[2].Parts[0].FunctionResponse.Response["ok"]; !ok {
		t.Errorf("parsed JSON response: %+v", contents[2].Parts[0].FunctionResponse.Response)
	}
	if contents[3].Parts[0].FunctionCall.Name != "grep" {
		t.Errorf("single tool call: %+v", contents[3])
	}
	if contents[4].Parts[0].FunctionResponse.Response["error"] != "tool failed" {
		t.Errorf("tool error: %+v", contents[4].Parts[0].FunctionResponse.Response)
	}
	if contents[5].Parts[0].Text != "final" {
		t.Errorf("final: %+v", contents[5])
	}
}

func TestToolCallToGeminiFuncCall_EmptyInput(t *testing.T) {
	call := toolCallToGeminiFuncCall("", "fn", "")
	if string(call.Args) != "{}" || call.ID != "" || call.Name != "fn" {
		t.Errorf("call = %+v", call)
	}
}

func TestToolResultToGeminiResponse_PlainAndError(t *testing.T) {
	plain := toolResultToGeminiResponse("hello", "")
	if plain["result"] != "hello" {
		t.Errorf("plain = %v", plain)
	}
	errResp := toolResultToGeminiResponse("out", "boom")
	if errResp["error"] != "boom" || errResp["output"] != "out" {
		t.Errorf("errResp = %v", errResp)
	}
}

func TestBuildGeminiTools_RequiredField(t *testing.T) {
	out := buildGeminiTools(map[string]tools.Schema{
		"t": {
			Description: "tool",
			Parameters: map[string]tools.Parameter{
				"q": {Type: "string", Required: true},
				"o": {Type: "string", Required: false},
			},
		},
	})
	if len(out) != 1 || len(out[0].FunctionDeclarations) != 1 {
		t.Fatalf("out = %+v", out)
	}
	req, _ := out[0].FunctionDeclarations[0].Parameters["required"].([]string)
	if len(req) != 1 || req[0] != "q" {
		t.Errorf("required = %v", out[0].FunctionDeclarations[0].Parameters["required"])
	}
}

func TestBuildGeminiTools_Empty(t *testing.T) {
	if buildGeminiTools(nil) != nil {
		t.Fatal("expected nil for empty schemas")
	}
}

func TestTranslateGeminiResponse_TextAndTools(t *testing.T) {
	resp := translateGeminiResponse(geminiResponse{
		Candidates: []struct {
			Content struct {
				Parts []geminiPart `json:"parts"`
				Role  string       `json:"role"`
			} `json:"content"`
			FinishReason string `json:"finishReason"`
		}{{
			FinishReason: "STOP",
			Content: struct {
				Parts []geminiPart `json:"parts"`
				Role  string       `json:"role"`
			}{
				Parts: []geminiPart{
					{Text: "hi"},
					{FunctionCall: &geminiFuncCall{Name: "t", Args: json.RawMessage(`{"k":1}`)}},
				},
			},
		}},
		UsageMetadata: struct {
			PromptTokenCount     int `json:"promptTokenCount"`
			CandidatesTokenCount int `json:"candidatesTokenCount"`
		}{PromptTokenCount: 5, CandidatesTokenCount: 3},
	})
	if resp.Content != "hi" || len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Input != `{"k":1}` {
		t.Errorf("resp = %+v", resp)
	}
	if resp.Usage.Total() != 8 {
		t.Errorf("usage = %d", resp.Usage.Total())
	}
}
