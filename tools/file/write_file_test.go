package file

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Ad3bay0c/routex/tools"
)

func TestWriteFile_BasicWrite(t *testing.T) {
	dir := t.TempDir()
	tool := WriteFileIn(dir)

	input := mustMarshal(t, map[string]any{
		"path":    "output.md",
		"content": "# Hello World",
	})

	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	var out map[string]any
	mustUnmarshal(t, result, &out)
	if out["success"] != true {
		t.Errorf("success = %v, want true", out["success"])
	}

	// Verify file was actually written
	content, err := os.ReadFile(filepath.Join(dir, "output.md"))
	if err != nil {
		t.Fatalf("file not written: %v", err)
	}
	if string(content) != "# Hello World" {
		t.Errorf("file content = %q, want %q", string(content), "# Hello World")
	}
}

func TestWriteFile_CreatesParentDirectories(t *testing.T) {
	dir := t.TempDir()
	tool := WriteFileIn(dir)

	input := mustMarshal(t, map[string]any{
		"path":    "subdir/nested/output.txt",
		"content": "nested content",
	})

	_, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "subdir/nested/output.txt")); err != nil {
		t.Errorf("nested file not created: %v", err)
	}
}

func TestWriteFile_AppendMode(t *testing.T) {
	dir := t.TempDir()
	tool := WriteFileIn(dir)

	// Write initial content
	mustExecute(t, tool, map[string]any{"path": "log.txt", "content": "line 1\n"})
	// Append
	mustExecute(t, tool, map[string]any{"path": "log.txt", "content": "line 2\n", "append": true})

	content, _ := os.ReadFile(filepath.Join(dir, "log.txt"))
	if !strings.Contains(string(content), "line 1") || !strings.Contains(string(content), "line 2") {
		t.Errorf("append failed, content = %q", string(content))
	}
}

func TestWriteFile_PathTraversalBlocked(t *testing.T) {
	dir := t.TempDir()
	tool := WriteFileIn(dir)

	cases := []string{
		"../escape.txt",
		"../../etc/passwd",
		"subdir/../../escape.txt",
	}
	for _, path := range cases {
		t.Run(path, func(t *testing.T) {
			input := mustMarshal(t, map[string]any{"path": path, "content": "attack"})
			_, err := tool.Execute(context.Background(), input)
			if err == nil {
				t.Errorf("path traversal %q should have been blocked", path)
			}
		})
	}
}

func TestWriteFile_MissingPath(t *testing.T) {
	tool := WriteFile()
	input := mustMarshal(t, map[string]any{"content": "hello"})
	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Fatal("should error when path is missing")
	}
	if err.Error() != "write_file: path is required" {
		t.Errorf("error = %v, want %v", err, "write_file: path is required")
	}
}

func TestWriteFile_MissingContent(t *testing.T) {
	dir := t.TempDir()
	tool := WriteFileIn(dir)
	input := mustMarshal(t, map[string]any{"path": "file.txt"})
	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Fatal("should error when content is missing")
	}
	if err.Error() != "write_file: content is required" {
		t.Errorf("error = %v, want %v", err, "write_file: content is required")
	}
}

func TestWriteFile_NameAndSchema(t *testing.T) {
	tool := WriteFile()
	if tool.Name() != "write_file" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "write_file")
	}
	schema := tool.Schema()
	if schema.Description == "" {
		t.Error("Schema.Description should not be empty")
	}
	if _, ok := schema.Parameters["path"]; !ok {
		t.Error("schema should have 'path' parameter")
	}
	if _, ok := schema.Parameters["content"]; !ok {
		t.Error("schema should have 'content' parameter")
	}
}

// TestWriteFile_FactoryHonorsBaseDir verifies the built-in factory sandboxes
// writes to cfg.BaseDir. Previously the factory ignored BaseDir, so a
// base_dir set in agents.yaml was a no-op and agents could write anywhere.
func TestWriteFile_FactoryHonorsBaseDir(t *testing.T) {
	dir := t.TempDir()
	tool, err := tools.Resolve("write_file", tools.ToolConfig{Name: "write_file", BaseDir: dir})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	// A write inside the sandbox lands under dir.
	in := mustMarshal(t, map[string]any{"path": "report.txt", "content": "ok"})
	if _, err := tool.Execute(context.Background(), in); err != nil {
		t.Fatalf("write inside base dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "report.txt")); err != nil {
		t.Fatalf("file not written into base dir: %v", err)
	}

	// A traversal attempt is rejected — the sandbox is actually enforced.
	esc := mustMarshal(t, map[string]any{"path": "../escape.txt", "content": "attack"})
	if _, err := tool.Execute(context.Background(), esc); err == nil {
		t.Fatal("traversal should be blocked when factory honors base_dir")
	}
}

// TestWriteFile_SiblingDirBypassBlocked guards against a raw prefix compare
// admitting a sibling directory whose name extends the base dir's name.
func TestWriteFile_SiblingDirBypassBlocked(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "out")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatalf("mkdir base: %v", err)
	}
	tool := WriteFileIn(base)

	// Resolves to <root>/output/x.txt — a sibling of the "out" sandbox that
	// shares its string prefix. Must be rejected.
	in := mustMarshal(t, map[string]any{"path": "../output/x.txt", "content": "attack"})
	if _, err := tool.Execute(context.Background(), in); err == nil {
		t.Fatal("sibling dir sharing the base name should be blocked")
	}
	if _, err := os.Stat(filepath.Join(root, "output", "x.txt")); err == nil {
		t.Fatal("file escaped the sandbox into a sibling directory")
	}
}
