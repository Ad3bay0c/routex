package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLI_NoArgs_PrintsUsage(t *testing.T) {
	c := newCLI()
	if err := c.run([]string{}); err != nil {
		t.Errorf("run() with no args should not error, got: %v", err)
	}
}

func TestCLI_Help_PrintsUsage(t *testing.T) {
	c := newCLI()
	for _, arg := range []string{"help", "--help", "-h"} {
		if err := c.run([]string{arg}); err != nil {
			t.Errorf("run(%q) should not error, got: %v", arg, err)
		}
	}
}

func TestCLI_UnknownCommand_ReturnsError(t *testing.T) {
	c := newCLI()
	err := c.run([]string{"notacommand"})
	if err == nil {
		t.Error("unknown command should return error")
	}
	if !strings.Contains(err.Error(), "unknown command") {
		t.Errorf("error = %q, should contain 'unknown command'", err.Error())
	}
}

func TestCLI_UnknownCommand_SuggestsCorrection(t *testing.T) {
	// Capture stderr — suggestions go there
	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	c := newCLI()
	c.run([]string{"runn"}) // typo of "run"

	w.Close()
	os.Stderr = oldStderr

	var sb strings.Builder
	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	sb.Write(buf[:n])
	output := sb.String()

	if !strings.Contains(output, "Did you mean") {
		t.Errorf("stderr should contain 'Did you mean', got: %q", output)
	}
	if !strings.Contains(output, "routex run") {
		t.Errorf("stderr should suggest 'routex run', got: %q", output)
	}
}

func TestCLI_Version_Dispatches(t *testing.T) {
	c := newCLI()
	for _, arg := range []string{"version", "--version", "-v"} {
		if err := c.run([]string{arg}); err != nil {
			t.Errorf("run(%q) should not error, got: %v", arg, err)
		}
	}
}

func TestParseFlags_LongForm(t *testing.T) {
	var val string
	flags := map[string]*string{"output": &val}

	pos, err := parseFlags([]string{"--output", "/tmp/out.md"}, flags)
	if err != nil {
		t.Fatalf("parseFlags error: %v", err)
	}
	if val != "/tmp/out.md" {
		t.Errorf("val = %q, want /tmp/out.md", val)
	}
	if len(pos) != 0 {
		t.Errorf("positional = %v, want empty", pos)
	}
}

func TestParseFlags_EqualsForm(t *testing.T) {
	var val string
	flags := map[string]*string{"output": &val}

	_, err := parseFlags([]string{"--output=/tmp/out.md"}, flags)
	if err != nil {
		t.Fatalf("parseFlags error: %v", err)
	}
	if val != "/tmp/out.md" {
		t.Errorf("val = %q, want /tmp/out.md", val)
	}
}

func TestParseFlags_ShortForm(t *testing.T) {
	var val string
	flags := map[string]*string{"o": &val, "output": &val}

	_, err := parseFlags([]string{"-o", "/tmp/out.md"}, flags)
	if err != nil {
		t.Fatalf("parseFlags error: %v", err)
	}
	if val != "/tmp/out.md" {
		t.Errorf("val = %q, want /tmp/out.md", val)
	}
}

func TestParseFlags_BoolFlag(t *testing.T) {
	var dryRun string
	flags := map[string]*string{"dry-run": &dryRun}

	_, err := parseFlags([]string{"--dry-run"}, flags)
	if err != nil {
		t.Fatalf("parseFlags error: %v", err)
	}
	if dryRun != "true" {
		t.Errorf("dryRun = %q, want true", dryRun)
	}
}

func TestParseFlags_PositionalArgs(t *testing.T) {
	var val string
	flags := map[string]*string{"output": &val}

	pos, err := parseFlags([]string{"agents.yaml", "--output", "out.md", "extra"}, flags)
	if err != nil {
		t.Fatalf("parseFlags error: %v", err)
	}
	if len(pos) != 2 || pos[0] != "agents.yaml" || pos[1] != "extra" {
		t.Errorf("positional = %v, want [agents.yaml extra]", pos)
	}
}

func TestParseFlags_HelpReturnsNil(t *testing.T) {
	flags := map[string]*string{}
	pos, err := parseFlags([]string{"--help"}, flags)
	if err != nil {
		t.Fatalf("parseFlags --help should not error, got: %v", err)
	}
	if pos != nil {
		t.Errorf("positional should be nil for --help, got: %v", pos)
	}
}

func TestParseFlags_FlagsOnlyReturnsEmptyNotNil(t *testing.T) {
	var dry string
	flags := map[string]*string{"dry-run": &dry}
	pos, err := parseFlags([]string{"--dry-run"}, flags)
	if err != nil {
		t.Fatal(err)
	}
	if pos == nil {
		t.Fatal("positional should be empty slice, not nil, when args were non-empty")
	}
	if len(pos) != 0 {
		t.Errorf("len = %d", len(pos))
	}
}

func TestParseFlags_UnknownFlag_ReturnsError(t *testing.T) {
	oldStderr := os.Stderr
	_, w, _ := os.Pipe()
	os.Stderr = w

	flags := map[string]*string{"output": new(string)}
	_, err := parseFlags([]string{"--unknown"}, flags)

	w.Close()
	os.Stderr = oldStderr

	if err == nil {
		t.Error("unknown flag should return error")
	}
	if !strings.Contains(err.Error(), "unknown flag") {
		t.Errorf("error = %q, should contain 'unknown flag'", err.Error())
	}
}

func TestParseFlags_UnknownFlag_SuggestsCorrection(t *testing.T) {
	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	flags := map[string]*string{"timeout": new(string)}
	parseFlags([]string{"--timout"}, flags) // typo

	w.Close()
	os.Stderr = oldStderr

	buf := make([]byte, 1024)
	n, _ := r.Read(buf)
	output := string(buf[:n])

	if !strings.Contains(output, "Did you mean") {
		t.Errorf("stderr should contain 'Did you mean', got: %q", output)
	}
	if !strings.Contains(output, "--timeout") {
		t.Errorf("stderr should suggest --timeout, got: %q", output)
	}
}

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "agents.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	return path
}

func minimalValidYAML() string {
	return `
runtime:
  name:         "test-crew"
  llm_provider: "anthropic"
  model:        "claude-haiku-4-5-20251001"
  api_key:      "test-key"

task:
  input:        "test task"
  max_duration: "5m"

agents:
  - id:      "worker"
    role:    "researcher"
    goal:    "complete the task"
    restart: "one_for_one"

memory:
  backend: "inmem"

observability:
  tracing: false
  metrics: false
`
}
