package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newOllamaTestServer returns a mock Ollama/OpenAI-compatible HTTP server:
// GET  /v1/models — ping for NewOllamaAdapter
// POST /v1        — chat completions (matches default baseURL used by inner OpenAI adapter)
func newOllamaTestServer(t *testing.T, chatBody map[string]any) *httptest.Server {
	t.Helper()
	data, err := json.Marshal(chatBody)
	if err != nil {
		t.Fatalf("marshal chat body: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(data)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newTestOllama(t *testing.T, srv *httptest.Server) *OllamaAdapter {
	t.Helper()
	a, err := NewOllamaAdapter(Config{
		Model:       "llama3",
		BaseURL:     srv.URL + "/v1",
		APIKey:      "",
		MaxTokens:   1024,
		Temperature: 0.7,
		Timeout:     5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewOllamaAdapter: %v", err)
	}
	return a
}

func TestOllama_MissingModel(t *testing.T) {
	_, err := NewOllamaAdapter(Config{Model: ""})
	if err == nil {
		t.Fatal("expected error for empty model")
	}
}

func TestOllama_ServerUnreachable(t *testing.T) {
	_, err := NewOllamaAdapter(Config{
		Model:   "llama3",
		BaseURL: "http://127.0.0.1:1/v1",
		Timeout: 2 * time.Second,
	})
	if err == nil {
		t.Fatal("expected error for unreachable server")
	}
	if !strings.Contains(err.Error(), "ollama") {
		t.Errorf("error should mention ollama: %v", err)
	}
}

func TestOllama_ProviderAndModel(t *testing.T) {
	srv := newOllamaTestServer(t, openAITextBody("x"))
	a := newTestOllama(t, srv)
	if a.Provider() != "ollama" {
		t.Errorf("Provider() = %q, want ollama", a.Provider())
	}
	if a.Model() != "llama3" {
		t.Errorf("Model() = %q, want llama3", a.Model())
	}
}

func TestOllama_Complete_DelegatesToOpenAICompatibleAPI(t *testing.T) {
	srv := newOllamaTestServer(t, openAITextBody("Hello Ollama"))
	resp, err := newTestOllama(t, srv).Complete(context.Background(), simpleRequest("hi"))
	if err != nil {
		t.Fatalf("Complete() error: %v", err)
	}
	if resp.Content != "Hello Ollama" {
		t.Errorf("Content = %q, want Hello Ollama", resp.Content)
	}
}

func TestOllama_Complete_ErrorWrapped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/models" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/v1" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]any{"message": "no", "type": "auth"},
			})
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	_, err := newTestOllama(t, srv).Complete(context.Background(), simpleRequest("hi"))
	if err == nil {
		t.Fatal("expected error from Complete")
	}
	if !strings.HasPrefix(err.Error(), "ollama:") {
		t.Errorf("error should have ollama: prefix, got %v", err)
	}
}
