package storage

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestReadS3_BasicRead(t *testing.T) {
	mock := &mockS3Getter{content: "hello from s3", contentType: "text/plain"}
	tool := &ReadS3Tool{bucket: "my-bucket", client: mock, maxBytes: 1024 * 1024}

	result, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"key": "reports/summary.md",
	}))
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	var out map[string]any
	mustUnmarshal(t, result, &out)

	if out["content"] != "hello from s3" {
		t.Errorf("content = %q, want %q", out["content"], "hello from s3")
	}
	if out["bucket"] != "my-bucket" {
		t.Errorf("bucket = %v, want my-bucket", out["bucket"])
	}
	if out["key"] != "reports/summary.md" {
		t.Errorf("key = %v, want reports/summary.md", out["key"])
	}
	if out["truncated"] != false {
		t.Errorf("truncated = %v, want false", out["truncated"])
	}
	if out["content_type"] != "text/plain" {
		t.Errorf("content_type = %v, want text/plain", out["content_type"])
	}
}

func TestReadS3_Truncation(t *testing.T) {
	content := strings.Repeat("a", 100)
	mock := &mockS3Getter{content: content}
	tool := &ReadS3Tool{bucket: "my-bucket", client: mock, maxBytes: 1024 * 1024}

	result, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"key":       "big.txt",
		"max_chars": 10,
	}))
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	var out map[string]any
	mustUnmarshal(t, result, &out)

	if out["truncated"] != true {
		t.Errorf("truncated = %v, want true", out["truncated"])
	}
	if len(out["content"].(string)) != 10 {
		t.Errorf("content len = %d, want 10", len(out["content"].(string)))
	}
}

func TestReadS3_ContentTypePassthrough(t *testing.T) {
	mock := &mockS3Getter{content: `{"ok":true}`, contentType: "application/json"}
	tool := &ReadS3Tool{bucket: "my-bucket", client: mock, maxBytes: 1024 * 1024}

	result, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"key": "data.json",
	}))
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	var out map[string]any
	mustUnmarshal(t, result, &out)

	if out["content_type"] != "application/json" {
		t.Errorf("content_type = %v, want application/json", out["content_type"])
	}
}

func TestReadS3_MissingKey(t *testing.T) {
	tool := &ReadS3Tool{bucket: "my-bucket", client: &mockS3Getter{}, maxBytes: 1024 * 1024}

	_, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{}))
	if err == nil {
		t.Fatal("should error when key is missing")
	}
	if err.Error() != "read_s3: key is required" {
		t.Errorf("error = %v, want read_s3: key is required", err)
	}
}

func TestReadS3_ClientError(t *testing.T) {
	mock := &mockS3Getter{err: errors.New("no such key")}
	tool := &ReadS3Tool{bucket: "my-bucket", client: mock, maxBytes: 1024 * 1024}

	_, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"key": "missing.txt",
	}))
	if err == nil {
		t.Fatal("should propagate S3 error")
	}
}

func TestReadS3_OversizedObjectRejected(t *testing.T) {
	mock := &mockS3Getter{content: "data", size: 2 * 1024 * 1024} // 2MB, over 1MB limit
	tool := &ReadS3Tool{bucket: "my-bucket", client: mock, maxBytes: 1024 * 1024}

	_, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"key": "large.bin",
	}))
	if err == nil {
		t.Fatal("should error for objects over the size limit")
	}
}

func TestReadS3_NameAndSchema(t *testing.T) {
	tool := &ReadS3Tool{bucket: "my-bucket", client: &mockS3Getter{}, maxBytes: 1024 * 1024}

	if tool.Name() != "read_s3" {
		t.Errorf("Name() = %q, want read_s3", tool.Name())
	}
	schema := tool.Schema()
	if schema.Description == "" {
		t.Error("Schema.Description should not be empty")
	}
	for _, param := range []string{"key", "max_chars"} {
		if _, ok := schema.Parameters[param]; !ok {
			t.Errorf("schema missing parameter %q", param)
		}
	}
}
