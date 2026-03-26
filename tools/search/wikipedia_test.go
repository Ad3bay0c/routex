package search

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func wikipediaSummary(title, extract string) map[string]any {
	return map[string]any{
		"title":   title,
		"extract": extract,
		"content_urls": map[string]any{
			"desktop": map[string]any{
				"page": "https://en.wikipedia.org/wiki/" + title,
			},
		},
	}
}

func TestWikipedia_FetchesSummary(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify the path contains the topic
		if !strings.Contains(r.URL.Path, "Go_programming_language") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(wikipediaSummary(
			"Go (programming language)",
			"Go is a statically typed, compiled language designed at Google.",
		))
	}))
	t.Cleanup(srv.Close)

	tool := &WikipediaTool{
		client:   srv.Client(),
		language: "en",
		baseURL:  srv.URL,
	}

	result, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"topic": "Go programming language",
	}))
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	var out map[string]any
	mustUnmarshal(t, result, &out)

	if out["title"] != "Go (programming language)" {
		t.Errorf("title = %q, want %q", out["title"], "Go (programming language)")
	}
	if !strings.Contains(out["summary"].(string), "Google") {
		t.Errorf("summary should contain expected content, got: %q", out["summary"])
	}
	if out["truncated"] != false {
		t.Errorf("truncated = %v, want false for short content", out["truncated"])
	}
}

func TestWikipedia_TruncatesLongSummary(t *testing.T) {
	longExtract := strings.Repeat("word ", 1000)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(wikipediaSummary("Test", longExtract))
	}))
	t.Cleanup(srv.Close)

	tool := &WikipediaTool{client: srv.Client(), language: "en", baseURL: srv.URL}

	result, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"topic":     "Test",
		"max_chars": 100,
	}))
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	var out map[string]any
	mustUnmarshal(t, result, &out)

	if out["truncated"] != true {
		t.Error("truncated = false, want true for long content")
	}
	if len(out["summary"].(string)) > 100 {
		t.Errorf("summary len = %d, want <= 100", len(out["summary"].(string)))
	}
}

func TestWikipedia_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]any{"type": "https://mediawiki.org/wiki/HyperSwitch/errors/not_found"})
	}))
	t.Cleanup(srv.Close)

	tool := &WikipediaTool{client: srv.Client(), language: "en", baseURL: srv.URL}

	_, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"topic": "NonexistentTopicXYZ123",
	}))
	if err == nil {
		t.Error("should error for 404 response")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should mention 'not found', got: %v", err)
	}
}

func TestWikipedia_MissingTopic(t *testing.T) {
	tool := Wikipedia()
	_, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{}))
	if err == nil {
		t.Error("should error when topic is missing")
	}
}

func TestWikipedia_NameAndSchema(t *testing.T) {
	tool := Wikipedia()
	if tool.Name() != "wikipedia" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "wikipedia")
	}
	if tool.Schema().Description == "" {
		t.Error("Schema.Description should not be empty")
	}
}
