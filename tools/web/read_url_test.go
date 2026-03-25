package web

import (
	"context"
	"strings"
	"testing"
)

func TestReadURL_FetchesPlainText(t *testing.T) {
	srv := newTestServer(t, 200, "<html><body><p>Hello World</p></body></html>", "text/html")

	tool := ReadURL()
	result, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"url": srv.URL,
	}))
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	var out map[string]any
	mustUnmarshal(t, result, &out)

	content := out["content"].(string)
	// HTML should be stripped
	if strings.Contains(content, "<html>") || strings.Contains(content, "<body>") {
		t.Errorf("HTML tags should be stripped, got: %q", content)
	}
	if !strings.Contains(content, "Hello World") {
		t.Errorf("content should contain text, got: %q", content)
	}
}

func TestReadURL_Truncation(t *testing.T) {
	bigContent := strings.Repeat("word ", 2000) // ~10000 chars
	srv := newTestServer(t, 200, bigContent, "text/html")

	tool := ReadURL()
	result, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"url":       srv.URL,
		"max_chars": 100,
	}))
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	var out map[string]any
	mustUnmarshal(t, result, &out)

	if out["truncated"] != true {
		t.Error("truncated = false, want true")
	}
}

func TestReadURL_HTTPError(t *testing.T) {
	srv := newTestServer(t, 404, "not found", "text/plain")

	tool := ReadURL()
	_, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"url": srv.URL,
	}))
	if err == nil {
		t.Fatal("should return error for 404 response")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error message should contain 404, got: %q", err.Error())
	}
}

func TestReadURL_MissingURL(t *testing.T) {
	tool := ReadURL()
	_, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{}))
	if err == nil {
		t.Error("should error when url is missing")
	}
}

func TestReadURL_NameAndSchema(t *testing.T) {
	tool := ReadURL()
	if tool.Name() != "read_url" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "read_url")
	}
	if tool.Schema().Description == "" {
		t.Error("Schema.Description should not be empty")
	}
}
