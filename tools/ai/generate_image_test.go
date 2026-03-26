package ai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateImage_SavesImageToDisk(t *testing.T) {
	dir := t.TempDir()

	// Create a small fake PNG
	fakePNG := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, // PNG signature
	}
	b64Image := base64.StdEncoding.EncodeToString(fakePNG)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"created": 1234567890,
			"data": []map[string]any{
				{"b64_json": b64Image, "revised_prompt": "A beautiful image"},
			},
		})
	}))
	t.Cleanup(srv.Close)

	tool := &GenerateImageTool{
		client:        srv.Client(),
		apiKey:        "test-key",
		baseDir:       dir,
		openAiBaseURL: srv.URL,
	}

	result, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"prompt":  "A test image",
		"save_as": "test.png",
	}))
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	var out generateImageOutput
	mustUnmarshal(t, result, &out)

	// File should exist on disk
	if _, err := os.Stat(filepath.Join(dir, "test.png")); err != nil {
		t.Errorf("image file not saved: %v", err)
	}
	if out.RevisedPrompt != "A beautiful image" {
		t.Errorf("RevisedPrompt = %q, want %q", out.RevisedPrompt, "A beautiful image")
	}
	if !strings.Contains(out.FilePath, "test.png") {
		t.Errorf("FilePath = %q, should contain test.png", out.FilePath)
	}
}

func TestGenerateImage_DefaultFilename(t *testing.T) {
	dir := t.TempDir()
	fakePNG := base64.StdEncoding.EncodeToString([]byte("fakeimage"))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"b64_json": fakePNG, "revised_prompt": ""}},
		})
	}))
	t.Cleanup(srv.Close)

	tool := &GenerateImageTool{
		client:        srv.Client(),
		apiKey:        "k",
		baseDir:       dir,
		openAiBaseURL: srv.URL,
	}

	result, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"prompt": "test",
		// no save_as — should generate a default name
	}))
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	var out generateImageOutput
	mustUnmarshal(t, result, &out)

	if out.FilePath == "" {
		t.Error("FilePath should not be empty")
	}
	if _, err := os.Stat(out.FilePath); err != nil {
		t.Errorf("file at FilePath %q does not exist: %v", out.FilePath, err)
	}
}

func TestGenerateImage_InvalidSize(t *testing.T) {
	tool := GenerateImage("key", t.TempDir())
	_, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"prompt": "test",
		"size":   "9999x9999",
	}))
	if err == nil {
		t.Fatal("should error for invalid size")
	}
	if !strings.Contains(err.Error(), "valid options: 1024x1024, 1792x1024, 1024x1792") {
		t.Errorf("invalid options: %q", err.Error())
	}
}

func TestGenerateImage_PathTraversalBlocked(t *testing.T) {
	fakePNG := base64.StdEncoding.EncodeToString([]byte("x"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"b64_json": fakePNG, "revised_prompt": ""}},
		})
	}))
	t.Cleanup(srv.Close)

	tool := &GenerateImageTool{
		client:        srv.Client(),
		apiKey:        "k",
		baseDir:       t.TempDir(),
		openAiBaseURL: srv.URL,
	}

	_, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"prompt":  "test",
		"save_as": "../escape.png",
	}))
	if err == nil {
		t.Error("path traversal in save_as should be blocked")
	}
}

func TestGenerateImage_InvalidAPIKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"invalid key"}`))
	}))
	t.Cleanup(srv.Close)

	tool := &GenerateImageTool{client: srv.Client(), apiKey: "bad", baseDir: t.TempDir(), openAiBaseURL: srv.URL}
	_, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{"prompt": "test"}))
	if err == nil {
		t.Error("should error for 401")
	}
}

func TestGenerateImage_MissingPrompt(t *testing.T) {
	tool := GenerateImage("key", t.TempDir())
	_, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{}))
	if err == nil {
		t.Error("should error when prompt is missing")
	}
}

func TestGenerateImage_NameAndSchema(t *testing.T) {
	tool := GenerateImage("key", "")
	if tool.Name() != "generate_image" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "generate_image")
	}
	if tool.Schema().Description == "" {
		t.Error("Schema.Description should not be empty")
	}
}
