package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestToolsCommand_NoSubcommand_ReturnsError(t *testing.T) {
	var out strings.Builder
	err := toolsCommandTo(&out, []string{})
	if err == nil {
		t.Error("toolsCommand with no subcommand should return error")
	}
}

func TestToolsCommand_UnknownSubcommand_ReturnsError(t *testing.T) {
	var out strings.Builder
	err := toolsCommandTo(&out, []string{"notasubcommand"})
	if err == nil {
		t.Error("unknown subcommand should return error")
	}
}

func TestToolsCommand_UnknownSubcommand_Suggests(t *testing.T) {
	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	var out strings.Builder
	toolsCommandTo(&out, []string{"lsit"}) // typo of "list"

	w.Close()
	os.Stderr = oldStderr

	buf := make([]byte, 1024)
	n, _ := r.Read(buf)
	if !strings.Contains(string(buf[:n]), "Did you mean") {
		t.Errorf("stderr should contain 'Did you mean', got: %q", string(buf[:n]))
	}
}

func TestToolsList_TableOutput(t *testing.T) {
	var out strings.Builder
	err := toolsListCommandTo(&out, []string{})
	if err != nil {
		t.Fatalf("toolsListCommandTo() error: %v", err)
	}

	output := out.String()
	for _, name := range []string{"web_search", "http_request", "write_file", "read_file", "wikipedia"} {
		if !strings.Contains(output, name) {
			t.Errorf("output should contain %q", name)
		}
	}
}

func TestToolsList_JSONOutput(t *testing.T) {
	var out strings.Builder
	err := toolsListCommandTo(&out, []string{"--json"})
	if err != nil {
		t.Fatalf("toolsListCommandTo --json error: %v", err)
	}

	var toolsList []struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal([]byte(out.String()), &toolsList); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", err, out.String())
	}
	if len(toolsList) == 0 {
		t.Error("tools list should not be empty")
	}

	names := make(map[string]bool)
	for _, tool := range toolsList {
		names[tool.Name] = true
	}
	for _, want := range []string{"web_search", "write_file", "read_file"} {
		if !names[want] {
			t.Errorf("tools list should contain %q", want)
		}
	}
}
