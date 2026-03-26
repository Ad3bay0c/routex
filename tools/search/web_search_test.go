package search

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebSearch_ReturnsResults(t *testing.T) {
	ddgResponse := map[string]any{
		"AbstractText": "Go is an open source programming language.",
		"RelatedTopics": []map[string]any{
			{"Text": "Go programming language - compiled, statically typed", "FirstURL": "https://golang.org"},
			{"Text": "Go documentation", "FirstURL": "https://pkg.go.dev"},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify the request has the expected query format
		if !strings.Contains(r.URL.RawQuery, "q=") {
			t.Errorf("request missing q= parameter, got: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ddgResponse)
	}))
	t.Cleanup(srv.Close)

	tool := &WebSearchTool{
		client:  srv.Client(),
		baseURL: srv.URL,
	}

	result, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"query": "Go programming language",
	}))
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	var out map[string]any
	mustUnmarshal(t, result, &out)

	if out["query"] != "Go programming language" {
		t.Errorf("query = %v, want Go programming language", out["query"])
	}
}

func TestWebSearch_MissingQuery(t *testing.T) {
	tool := WebSearch()
	_, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{}))
	if err == nil {
		t.Error("should error when query is missing")
	}
}

func TestWebSearch_NameAndSchema(t *testing.T) {
	tool := WebSearch()
	if tool.Name() != "web_search" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "web_search")
	}
	schema := tool.Schema()
	if schema.Description == "" {
		t.Error("Schema.Description should not be empty")
	}
	if _, ok := schema.Parameters["query"]; !ok {
		t.Error("schema should have 'query' parameter")
	}
}
