package search

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func braveResponse(results []map[string]any) map[string]any {
	return map[string]any{
		"web": map[string]any{
			"results": results,
		},
	}
}

func TestBraveSearch_ReturnsResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify auth header
		if r.Header.Get("X-Subscription-Token") == "" {
			t.Error("missing X-Subscription-Token header")
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(braveResponse([]map[string]any{
			{"title": "Go 1.26 Release Notes", "url": "https://go.dev/doc/go1.26", "description": "What's new in Go 1.26"},
			{"title": "Go Blog", "url": "https://go.dev/blog", "description": "The official Go blog"},
		}))
	}))
	t.Cleanup(srv.Close)

	tool := &BraveSearchTool{
		client:     srv.Client(),
		apiKey:     "test-key",
		maxResults: 5,
		baseURL:    srv.URL,
	}

	result, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"query": "Go 1.26",
	}))
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	var out map[string]any
	mustUnmarshal(t, result, &out)

	results := out["results"].([]any)
	if len(results) != 2 {
		t.Errorf("results len = %d, want 2", len(results))
	}
	if out["total"].(float64) != 2 {
		t.Errorf("total = %v, want 2", out["total"])
	}
}

func TestBraveSearch_RespectsMaxResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return 10 results from the API
		results := make([]map[string]any, 10)
		for i := range results {
			results[i] = map[string]any{"title": "result", "url": "https://example.com", "description": "desc"}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(braveResponse(results))
	}))
	t.Cleanup(srv.Close)

	tool := &BraveSearchTool{
		client:     srv.Client(),
		apiKey:     "test-key",
		maxResults: 3,
		baseURL:    srv.URL,
	}

	result, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"query": "test",
	}))
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	var out map[string]any
	mustUnmarshal(t, result, &out)

	// Should be capped at maxResults=3
	results := out["results"].([]any)
	if len(results) != 3 {
		t.Errorf("results len = %d, want 3 (capped by maxResults)", len(results))
	}
}

func TestBraveSearch_InvalidAPIKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)

	tool := &BraveSearchTool{
		client:     srv.Client(),
		apiKey:     "bad-key",
		maxResults: 5,
		baseURL:    srv.URL,
	}

	_, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{"query": "test"}))
	if err == nil {
		t.Error("should error for 401 response")
	}
}

func TestBraveSearch_MissingQuery(t *testing.T) {
	tool := BraveSearch("key")
	_, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{}))
	if err == nil {
		t.Error("should error when query is missing")
	}
}

func TestBraveSearch_NameAndSchema(t *testing.T) {
	tool := BraveSearch("key")
	if tool.Name() != "brave_search" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "brave_search")
	}
	if tool.Schema().Description == "" {
		t.Error("Schema.Description should not be empty")
	}
}
