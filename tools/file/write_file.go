package file

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Ad3bay0c/routex/tools"
)

func init() {
	tools.RegisterBuiltin("write_file", func(cfg tools.ToolConfig) (tools.Tool, error) {
		return WriteFile(), nil
	})
}

// WriteFileTool saves text content to a file on disk.
// Used by writer and critic agents to persist their output.
// The LLM decides the filename and content — we just write it safely.
type WriteFileTool struct {
	// baseDir restricts where files can be written.
	// Empty means the current working directory.
	// Set this to a specific folder to sandbox agent file writes.
	baseDir string
}

// writeFileInput is the shape of JSON the LLM sends when calling this tool.
type writeFileInput struct {
	// Path is the file path to write to.
	// Example: "report.md", "outputs/summary.txt"
	Path string `json:"path"`

	// Content is the text to write into the file.
	Content string `json:"content"`

	// Append controls whether to add to an existing file (true)
	// or replace it entirely (false). Defaults to false.
	Append bool `json:"append,omitempty"`
}

// writeFileOutput is the response we send back to the LLM.
type writeFileOutput struct {
	Success bool   `json:"success"`
	Path    string `json:"path"`
	Bytes   int    `json:"bytes_written"`
	Message string `json:"message"`
}

// WriteFile returns a ready-to-use WriteFileTool that writes to the
// current working directory. For a sandboxed version use WriteFileIn().
func WriteFile() *WriteFileTool {
	return &WriteFileTool{baseDir: ""}
}

// WriteFileIn returns a WriteFileTool that can only write files
// inside the given base directory. Use this in production to prevent
// agents from writing files outside an intended output folder.
//
// Example:
//
//	rt.RegisterTool(tools.WriteFileIn("./outputs"))
func WriteFileIn(baseDir string) *WriteFileTool {
	return &WriteFileTool{baseDir: baseDir}
}

// Name returns the tool identifier.
// This satisfies the Tool interface.
func (t *WriteFileTool) Name() string {
	return "write_file"
}

// Schema describes this tool to the LLM.
// This satisfies the Tool interface.
func (t *WriteFileTool) Schema() tools.Schema {
	return tools.Schema{
		Description: "Write text content to a file on disk. " +
			"Use this to save reports, summaries, or any output that should persist. " +
			"Creates parent directories automatically if they do not exist.",
		Parameters: map[string]tools.Parameter{
			"path": {
				Type:        "string",
				Description: "File path to write to. Example: 'report.md' or 'outputs/summary.txt'",
				Required:    true,
			},
			"content": {
				Type:        "string",
				Description: "The full text content to write into the file.",
				Required:    true,
			},
			"append": {
				Type:        "boolean",
				Description: "If true, add content to the end of the file instead of replacing it. Defaults to false.",
				Required:    false,
			},
		},
	}
}

// Execute writes content to a file when the LLM requests it.
// This satisfies the Tool interface.
func (t *WriteFileTool) Execute(_ context.Context, input json.RawMessage) (json.RawMessage, error) {
	// — parse the LLM's input
	var params writeFileInput
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("write_file: invalid input: %w", err)
	}

	if params.Path == "" {
		return nil, fmt.Errorf("write_file: path is required")
	}
	if params.Content == "" {
		return nil, fmt.Errorf("write_file: content is required")
	}

	// — resolve and sanitise the file path
	// Clean removes .., double slashes, and other path tricks
	cleanPath := filepath.Clean(params.Path)

	// If a base directory is set, join it and verify the result
	// stays inside it — prevents path traversal (../../etc/passwd tricks)
	if t.baseDir != "" {
		fullPath := filepath.Join(t.baseDir, cleanPath)
		absBase, err := filepath.Abs(t.baseDir)
		if err != nil {
			return nil, fmt.Errorf("write_file: resolve base dir: %w", err)
		}
		absFull, err := filepath.Abs(fullPath)
		if err != nil {
			return nil, fmt.Errorf("write_file: resolve path: %w", err)
		}
		// Ensure the resolved path starts with the base directory
		// This is the path traversal prevention check
		if len(absFull) < len(absBase) || absFull[:len(absBase)] != absBase {
			return nil, fmt.Errorf(
				"write_file: path %q is outside the allowed directory %q",
				params.Path, t.baseDir,
			)
		}
		cleanPath = fullPath
	}

	// — create parent directories if they do not exist
	// MkdirAll is like `mkdir -p` — creates the full path, no error if exists
	dir := filepath.Dir(cleanPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("write_file: create directories for %q: %w", cleanPath, err)
	}

	// — write the file
	var writeErr error
	if params.Append {
		// Open for appending — create if not exists, append if exists
		f, err := os.OpenFile(cleanPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return nil, fmt.Errorf("write_file: open for append %q: %w", cleanPath, err)
		}
		defer f.Close()
		_, writeErr = f.WriteString(params.Content)
	} else {
		// WriteFile creates or truncates — clean overwrite
		writeErr = os.WriteFile(cleanPath, []byte(params.Content), 0644)
	}

	if writeErr != nil {
		return nil, fmt.Errorf("write_file: write %q: %w", cleanPath, writeErr)
	}

	// Step 5 — return success confirmation to the LLM
	output := writeFileOutput{
		Success: true,
		Path:    cleanPath,
		Bytes:   len(params.Content),
		Message: fmt.Sprintf("Successfully wrote %d bytes to %s", len(params.Content), cleanPath),
	}

	return json.Marshal(output)
}

// compile-time interface check
var _ tools.Tool = (*WriteFileTool)(nil)
