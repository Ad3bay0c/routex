package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateCommand_ValidConfig(t *testing.T) {
	configPath := writeTempConfig(t, minimalValidYAML())

	var out strings.Builder
	err := validateCommandTo(&out, []string{configPath})
	if err != nil {
		t.Fatalf("validateCommandTo() should succeed for valid config, got: %v", err)
	}
	if !strings.Contains(out.String(), "valid") {
		t.Errorf("output should confirm config is valid, got: %q", out.String())
	}
}

func TestValidateCommand_InvalidConfig(t *testing.T) {
	configPath := writeTempConfig(t, `
runtime:
  name: "test"
agents:
  - id: "a"
    role: "not-a-real-role"
`)
	var out strings.Builder
	err := validateCommandTo(&out, []string{configPath})
	if err == nil {
		t.Error("validateCommandTo() should fail for invalid config")
	}
}

func TestValidateCommand_MissingFile(t *testing.T) {
	var out strings.Builder
	err := validateCommandTo(&out, []string{"/nonexistent/path/agents.yaml"})
	if err == nil {
		t.Error("validateCommandTo() should fail for missing file")
	}
}

func TestValidateCommand_MissingArg(t *testing.T) {
	var out strings.Builder
	err := validateCommandTo(&out, []string{})
	if err != nil {
		t.Error("validateCommandTo() should fail when no file path given")
	}
	if !strings.Contains(out.String(), "routex validate <agents.yaml> [flags]") {
		t.Error("validateCommandTo() should fail when no flags given")
	}
}

func TestValidateCommand_JSONOutput_ValidConfig(t *testing.T) {
	configPath := writeTempConfig(t, minimalValidYAML())

	var out strings.Builder
	err := validateCommandTo(&out, []string{configPath, "--json"})
	if err != nil {
		t.Fatalf("validateCommandTo --json should succeed for valid config, got: %v", err)
	}

	var result struct {
		Valid bool   `json:"valid"`
		File  string `json:"file"`
		Error string `json:"error,omitempty"`
	}
	if err := json.Unmarshal([]byte(out.String()), &result); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", err, out.String())
	}
	if !result.Valid {
		t.Errorf("valid = false, want true")
	}
}

func TestValidateCommand_JSONOutput_InvalidConfig(t *testing.T) {
	configPath := writeTempConfig(t, `runtime: {}`)

	var out strings.Builder
	_ = validateCommandTo(&out, []string{configPath, "--json"})

	var result struct {
		Valid bool   `json:"valid"`
		Error string `json:"error,omitempty"`
	}
	if err := json.Unmarshal([]byte(out.String()), &result); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", err, out.String())
	}
	if result.Valid {
		t.Error("valid = true for invalid config, want false")
	}
	if result.Error == "" {
		t.Error("error should be non-empty for invalid config")
	}
}
