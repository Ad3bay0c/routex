package storage

import (
	"context"
	"errors"
	"testing"
)

func TestWriteS3_BasicWrite(t *testing.T) {
	mock := &mockS3Putter{}
	tool := &WriteS3Tool{bucket: "my-bucket", client: mock}

	result, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"key":     "reports/output.md",
		"content": "# Hello S3",
	}))
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	var out map[string]any
	mustUnmarshal(t, result, &out)

	if out["success"] != true {
		t.Errorf("success = %v, want true", out["success"])
	}
	if out["bucket"] != "my-bucket" {
		t.Errorf("bucket = %v, want my-bucket", out["bucket"])
	}
	if out["key"] != "reports/output.md" {
		t.Errorf("key = %v, want reports/output.md", out["key"])
	}
	if out["uri"] != "s3://my-bucket/reports/output.md" {
		t.Errorf("uri = %v, want s3://my-bucket/reports/output.md", out["uri"])
	}
	if mock.capturedKey != "reports/output.md" {
		t.Errorf("PutObject key = %q, want %q", mock.capturedKey, "reports/output.md")
	}
	if mock.capturedBody != "# Hello S3" {
		t.Errorf("PutObject body = %q, want %q", mock.capturedBody, "# Hello S3")
	}
}

func TestWriteS3_DefaultContentType(t *testing.T) {
	mock := &mockS3Putter{}
	tool := &WriteS3Tool{bucket: "my-bucket", client: mock}

	_, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"key":     "file.txt",
		"content": "hello",
	}))
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	if mock.capturedContentType != "text/plain" {
		t.Errorf("content_type = %q, want text/plain", mock.capturedContentType)
	}
}

func TestWriteS3_CustomContentType(t *testing.T) {
	mock := &mockS3Putter{}
	tool := &WriteS3Tool{bucket: "my-bucket", client: mock}

	_, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"key":          "data.json",
		"content":      `{"key":"value"}`,
		"content_type": "application/json",
	}))
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	if mock.capturedContentType != "application/json" {
		t.Errorf("content_type = %q, want application/json", mock.capturedContentType)
	}
}

func TestWriteS3_MissingKey(t *testing.T) {
	tool := &WriteS3Tool{bucket: "my-bucket", client: &mockS3Putter{}}

	_, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"content": "hello",
	}))
	if err == nil {
		t.Fatal("should error when key is missing")
	}
	if err.Error() != "write_s3: key is required" {
		t.Errorf("error = %v, want write_s3: key is required", err)
	}
}

func TestWriteS3_MissingContent(t *testing.T) {
	tool := &WriteS3Tool{bucket: "my-bucket", client: &mockS3Putter{}}

	_, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"key": "file.txt",
	}))
	if err == nil {
		t.Fatal("should error when content is missing")
	}
	if err.Error() != "write_s3: content is required" {
		t.Errorf("error = %v, want write_s3: content is required", err)
	}
}

func TestWriteS3_ClientError(t *testing.T) {
	mock := &mockS3Putter{err: errors.New("access denied")}
	tool := &WriteS3Tool{bucket: "my-bucket", client: mock}

	_, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"key":     "file.txt",
		"content": "hello",
	}))
	if err == nil {
		t.Fatal("should propagate S3 error")
	}
}

func TestWriteS3_NameAndSchema(t *testing.T) {
	tool := &WriteS3Tool{bucket: "my-bucket", client: &mockS3Putter{}}

	if tool.Name() != "write_s3" {
		t.Errorf("Name() = %q, want write_s3", tool.Name())
	}
	schema := tool.Schema()
	if schema.Description == "" {
		t.Error("Schema.Description should not be empty")
	}
	for _, param := range []string{"key", "content", "content_type"} {
		if _, ok := schema.Parameters[param]; !ok {
			t.Errorf("schema missing parameter %q", param)
		}
	}
}
