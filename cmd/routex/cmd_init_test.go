package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitCommand_CreatesFiles(t *testing.T) {
	dir := t.TempDir()
	if err := initCommand([]string{dir}); err != nil {
		t.Fatalf("initCommand() error: %v", err)
	}

	for _, name := range []string{"agents.yaml", ".env.example", ".gitignore", "main.go"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("expected file %q not created: %v", name, err)
		}
	}
}

func TestInitCommand_AgentsYAMLContent(t *testing.T) {
	dir := t.TempDir()
	initCommand([]string{dir})

	content, err := os.ReadFile(filepath.Join(dir, "agents.yaml"))
	if err != nil {
		t.Fatalf("read agents.yaml: %v", err)
	}
	yaml := string(content)

	for _, required := range []string{"runtime:", "task:", "agents:", "tools:"} {
		if !strings.Contains(yaml, required) {
			t.Errorf("agents.yaml missing %q section", required)
		}
	}
	if !strings.Contains(yaml, "env:") {
		t.Error("agents.yaml should use env: prefix for API keys")
	}
}

func TestInitCommand_DoesNotOverwriteExistingFiles(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "agents.yaml")
	os.WriteFile(existing, []byte("# existing content"), 0644)

	initCommand([]string{dir})

	content, _ := os.ReadFile(existing)
	if string(content) != "# existing content" {
		t.Error("init should not overwrite existing agents.yaml")
	}
}

func TestInitCommand_CurrentDirectory(t *testing.T) {
	dir := t.TempDir()
	orig, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(orig)

	if err := initCommand([]string{}); err != nil {
		t.Fatalf("initCommand() with no args error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "agents.yaml")); err != nil {
		t.Error("agents.yaml not created in current directory")
	}
}

func TestInitCommand_CreatesNestedDirectory(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "sub", "nested")

	if err := initCommand([]string{dir}); err != nil {
		t.Fatalf("initCommand() error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "agents.yaml")); err != nil {
		t.Error("agents.yaml not created in nested directory")
	}
}

func TestInitCommand_ProjectNameFromDirectory(t *testing.T) {
	dir := t.TempDir()
	projectDir := filepath.Join(dir, "my-research-crew")
	os.MkdirAll(projectDir, 0755)

	initCommand([]string{projectDir})

	content, _ := os.ReadFile(filepath.Join(projectDir, "agents.yaml"))
	if !strings.Contains(string(content), "my-research-crew") {
		t.Error("agents.yaml should use directory name as project name")
	}
}

func TestInitCommand_Help(t *testing.T) {
	if err := initCommand([]string{"--help"}); err != nil {
		t.Errorf("init --help should not error, got: %v", err)
	}
}
