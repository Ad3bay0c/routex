package file

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadFile_MissingPath(t *testing.T) {
	tool := ReadFile()
	input := mustMarshal(t, map[string]any{})
	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Fatal("should error when path is missing")
	}
	if err.Error() != "read_file: path is required" {
		t.Errorf("error = %v, want %v", err, "read_file: path is required")
	}
}

func TestReadFile_FileNotFound(t *testing.T) {
	dir := t.TempDir()
	tool := ReadFileFrom(dir)
	input := mustMarshal(t, map[string]any{"path": "nonexistent.txt"})

	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Error("should error for missing file")
	}
}

func TestReadFile_BasicRead(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "data.txt")
	os.WriteFile(filePath, []byte("hello routex"), 0644)

	tool := ReadFileFrom(dir)
	input := mustMarshal(t, map[string]any{"path": "data.txt"})

	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	var out map[string]any
	mustUnmarshal(t, result, &out)

	if out["content"] != "hello routex" {
		t.Errorf("content = %q, want %q", out["content"], "hello routex")
	}
	if out["truncated"] != false {
		t.Errorf("truncated = %v, want false", out["truncated"])
	}
}

func TestReadFile_Truncation(t *testing.T) {
	dir := t.TempDir()

	content := strings.Repeat("a", 100)
	os.WriteFile(filepath.Join(dir, "big.txt"), []byte(content), 0644)

	tool := ReadFileFrom(dir)
	input := mustMarshal(t, map[string]any{"path": "big.txt", "max_chars": 10})

	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	var out map[string]any
	mustUnmarshal(t, result, &out)

	if out["truncated"] != true {
		t.Errorf("truncated = %v, want true", out["truncated"])
	}
	returnedContent := out["content"].(string)
	if len(returnedContent) != 10 {
		t.Errorf("content len = %d, want 10", len(returnedContent))
	}
}

func TestReadFile_PathTraversalBlocked(t *testing.T) {
	dir := t.TempDir()

	sensitiveDir := filepath.Dir(dir)
	os.WriteFile(filepath.Join(sensitiveDir, "secret.txt"), []byte("secret"), 0644)

	tool := ReadFileFrom(dir)
	input := mustMarshal(t, map[string]any{"path": "../secret.txt"})

	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Fatal("path traversal should have been blocked")
	}
	if !strings.Contains(err.Error(), "path \"../secret.txt\" is outside the allowed directory") {
		t.Errorf("error = %v, want %v", err, "path \"../secret.txt\" is outside the allowed directory")
	}
}

func TestReadFile_DirectoryRejected(t *testing.T) {
	dir := t.TempDir()
	subDir := filepath.Join(dir, "subdir")
	os.MkdirAll(subDir, 0755)

	tool := ReadFileFrom(dir)
	input := mustMarshal(t, map[string]any{"path": "subdir"})

	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Fatal("should error when path is a directory")
	}
	if !strings.Contains(err.Error(), "is a directory, not a file") {
		t.Errorf("error = %v, want %v", err, "is a directory, not a file")
	}
}

func TestReadFile_NameAndSchema(t *testing.T) {
	tool := ReadFile()
	if tool.Name() != "read_file" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "read_file")
	}
	schema := tool.Schema()
	if schema.Description == "" {
		t.Error("Schema.Description should not be empty")
	}
	if _, ok := schema.Parameters["path"]; !ok {
		t.Error("schema should have 'path' parameter")
	}
}
