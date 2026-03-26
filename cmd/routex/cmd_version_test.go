package main

import (
	"encoding/json"
	"runtime"
	"strings"
	"testing"
)

func TestVersionCommand_PlainOutput(t *testing.T) {
	var out strings.Builder
	err := versionCommandTo(&out, []string{})
	if err != nil {
		t.Fatalf("versionCommandTo() error: %v", err)
	}

	output := out.String()
	if !strings.Contains(output, "routex") {
		t.Errorf("output should contain 'routex', got: %q", output)
	}
	if !strings.Contains(output, runtime.Version()) {
		t.Errorf("output should contain Go version, got: %q", output)
	}
	if !strings.Contains(output, runtime.GOOS) {
		t.Errorf("output should contain OS, got: %q", output)
	}
}

func TestVersionCommand_JSONOutput(t *testing.T) {
	var out strings.Builder
	err := versionCommandTo(&out, []string{"--json"})
	if err != nil {
		t.Fatalf("versionCommandTo --json error: %v", err)
	}

	var info struct {
		Version   string `json:"version"`
		GoVersion string `json:"go_version"`
		OS        string `json:"os"`
		Arch      string `json:"arch"`
	}
	if err := json.Unmarshal([]byte(out.String()), &info); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", err, out.String())
	}
	if info.GoVersion != runtime.Version() {
		t.Errorf("go_version = %q, want %q", info.GoVersion, runtime.Version())
	}
	if info.OS != runtime.GOOS {
		t.Errorf("os = %q, want %q", info.OS, runtime.GOOS)
	}
	if info.Arch != runtime.GOARCH {
		t.Errorf("arch = %q, want %q", info.Arch, runtime.GOARCH)
	}
}
