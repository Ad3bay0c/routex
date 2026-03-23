package file

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"unicode/utf8"

	"github.com/Ad3bay0c/routex/tools"
)

// ReadFileTool reads the content of a local file.
// The complement to write_file — agents can read existing files,
// process their contents, and feed them into the pipeline.
// Uses the same path sandboxing as write_file for safety.
//
// agents.yaml:
//
//	tools:
//	  - name: "read_file"
//	    base_dir: "./data"   # optional: restrict reads to this folder
type ReadFileTool struct {
	baseDir  string
	maxBytes int
}

type readFileInput struct {
	Path     string `json:"path"`
	MaxChars int    `json:"max_chars,omitempty"`
}

type readFileOutput struct {
	Path      string `json:"path"`
	Content   string `json:"content"`
	Size      int    `json:"size_bytes"`
	CharCount int    `json:"char_count"`
	Truncated bool   `json:"truncated"`
}

// ReadFile returns a ReadFileTool with no directory restriction.
func ReadFile() *ReadFileTool {
	return &ReadFileTool{maxBytes: 1024 * 1024} // 1MB limit
}

// ReadFileFrom returns a ReadFileTool restricted to the given directory.
func ReadFileFrom(baseDir string) *ReadFileTool {
	return &ReadFileTool{baseDir: baseDir, maxBytes: 1024 * 1024}
}

func (t *ReadFileTool) Name() string { return "read_file" }

func (t *ReadFileTool) Schema() tools.Schema {
	return tools.Schema{
		Description: "Read the content of a local file. " +
			"Use to load existing documents, configuration files, data files, or previously saved reports. " +
			"Returns the file content as text.",
		Parameters: map[string]tools.Parameter{
			"path": {
				Type:        "string",
				Description: "Path to the file to read. Example: 'data/report.md', 'config.json'",
				Required:    true,
			},
			"max_chars": {
				Type:        "number",
				Description: "Maximum characters to return. Default: 8000. Increase for larger files.",
				Required:    false,
			},
		},
	}
}

func (t *ReadFileTool) Execute(_ context.Context, input json.RawMessage) (json.RawMessage, error) {
	var params readFileInput
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("read_file: invalid input: %w", err)
	}
	if params.Path == "" {
		return nil, fmt.Errorf("read_file: path is required")
	}

	maxChars := params.MaxChars
	if maxChars <= 0 {
		maxChars = 8000
	}

	// Resolve and sanitise path — same traversal prevention as write_file
	cleanPath := filepath.Clean(params.Path)

	if t.baseDir != "" {
		fullPath := filepath.Join(t.baseDir, cleanPath)
		absBase, err := filepath.Abs(t.baseDir)
		if err != nil {
			return nil, fmt.Errorf("read_file: resolve base dir: %w", err)
		}
		absFull, err := filepath.Abs(fullPath)
		if err != nil {
			return nil, fmt.Errorf("read_file: resolve path: %w", err)
		}
		if len(absFull) < len(absBase) || absFull[:len(absBase)] != absBase {
			return nil, fmt.Errorf(
				"read_file: path %q is outside the allowed directory %q",
				params.Path, t.baseDir,
			)
		}
		cleanPath = fullPath
	}

	// Check the file exists before trying to read
	info, err := os.Stat(cleanPath)
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("read_file: file %q does not exist", cleanPath)
	}
	if err != nil {
		return nil, fmt.Errorf("read_file: stat %q: %w", cleanPath, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("read_file: %q is a directory, not a file", cleanPath)
	}

	// Enforce size limit before reading
	if info.Size() > int64(t.maxBytes) {
		return nil, fmt.Errorf(
			"read_file: file %q is %d bytes, limit is %d — use max_chars to read a portion",
			cleanPath, info.Size(), t.maxBytes,
		)
	}

	data, err := os.ReadFile(cleanPath)
	if err != nil {
		return nil, fmt.Errorf("read_file: read %q: %w", cleanPath, err)
	}

	content := string(data)
	truncated := false

	if utf8.RuneCountInString(content) > maxChars {
		runes := []rune(content)
		content = string(runes[:maxChars])
		truncated = true
	}

	return json.Marshal(readFileOutput{
		Path:      cleanPath,
		Content:   content,
		Size:      int(info.Size()),
		CharCount: utf8.RuneCountInString(content),
		Truncated: truncated,
	})
}

func init() {
	tools.RegisterBuiltin("read_file", func(cfg tools.ToolConfig) (tools.Tool, error) {
		if cfg.BaseDir != "" {
			return ReadFileFrom(cfg.BaseDir), nil
		}
		return ReadFile(), nil
	})
}

var _ tools.Tool = (*ReadFileTool)(nil)
