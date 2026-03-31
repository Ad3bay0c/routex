package llm

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Ad3bay0c/routex/memory"
)

func mockServer(t *testing.T, status int, body any) *httptest.Server {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("mockServer: marshal body: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		w.Write(data)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func mockServerFunc(t *testing.T, fn http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(fn)
	t.Cleanup(srv.Close)
	return srv
}

func simpleHistory(content string) []memory.Message {
	return []memory.Message{
		{Role: "user", Content: content, Timestamp: time.Now()},
	}
}

func simpleRequest(input string) Request {
	return Request{
		SystemPrompt: "You are a helpful assistant.",
		History:      simpleHistory(input),
	}
}

func TestNew_ValidProviders(t *testing.T) {
	tests := []struct {
		provider     string
		wantProvider string
	}{
		{"anthropic", "anthropic"},
		{"openai", "openai"},
	}

	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			adapter, err := New(Config{
				Provider: tt.provider,
				Model:    "test-model",
				APIKey:   "test-key",
			})
			if err != nil {
				t.Fatalf("New() error: %v", err)
			}
			if adapter.Provider() != tt.wantProvider {
				t.Errorf("Provider() = %q, want %q", adapter.Provider(), tt.wantProvider)
			}
		})
	}
}

func TestNew_UnknownProvider(t *testing.T) {
	_, err := New(Config{Provider: "grok", Model: "grok-pro", APIKey: "key"})
	if err == nil {
		t.Fatal("New() should error for unknown provider")
	}
}

func TestNew_EmptyProvider(t *testing.T) {
	_, err := New(Config{Model: "some-model", APIKey: "key"})
	if err == nil {
		t.Fatal("New() should error for empty provider")
	}
}

func TestTokenUsage_Total(t *testing.T) {
	u := TokenUsage{InputTokens: 100, OutputTokens: 50}
	if u.Total() != 150 {
		t.Errorf("Total() = %d, want 150", u.Total())
	}
}

func TestTokenUsage_ZeroValues(t *testing.T) {
	var u TokenUsage
	if u.Total() != 0 {
		t.Errorf("Total() on zero value = %d, want 0", u.Total())
	}
}
