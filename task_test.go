package routex

import (
	"testing"
	"time"
)

func TestTask_ZeroValue(t *testing.T) {
	var task Task

	if task.Input != "" {
		t.Errorf("zero-value Task.Input should be empty, got %q", task.Input)
	}
	if task.MaxDuration != 0 {
		t.Errorf("zero-value Task.MaxDuration should be 0, got %v", task.MaxDuration)
	}
	if task.OutputFile != "" {
		t.Errorf("zero-value Task.OutputFile should be empty, got %q", task.OutputFile)
	}
	if task.Metadata != nil {
		t.Errorf("zero-value Task.Metadata should be nil, got %v", task.Metadata)
	}
}

func TestTask_Fields(t *testing.T) {
	task := Task{
		Input:       "Write a report",
		MaxDuration: 5 * time.Minute,
		OutputFile:  "report.md",
		Metadata:    map[string]string{"lang": "en"},
	}

	if task.Input != "Write a report" {
		t.Errorf("expected Input %q, got %q", "Write a report", task.Input)
	}
	if task.MaxDuration != 5*time.Minute {
		t.Errorf("expected MaxDuration 5m, got %v", task.MaxDuration)
	}
	if task.OutputFile != "report.md" {
		t.Errorf("expected OutputFile %q, got %q", "report.md", task.OutputFile)
	}
	if task.Metadata["lang"] != "en" {
		t.Errorf("expected Metadata lang=en, got %q", task.Metadata["lang"])
	}
}

func TestResult_ZeroValue(t *testing.T) {
	var result Result

	if result.Output != "" {
		t.Errorf("zero-value Result.Output should be empty")
	}
	if result.TokensUsed != 0 {
		t.Errorf("zero-value Result.TokensUsed should be 0")
	}
	if result.Error != nil {
		t.Errorf("zero-value Result.Error should be nil")
	}
	if result.AgentResults != nil {
		t.Errorf("zero-value Result.AgentResults should be nil")
	}
}

func TestToolCall_ErrorField(t *testing.T) {
	// ToolCall.Error is a real error value — verify nil means success
	tc := ToolCall{
		ToolName: "web_search",
		Input:    `{"query":"golang"}`,
		Output:   `{"results":[]}`,
		Duration: 200 * time.Millisecond,
		Error:    nil,
	}

	if tc.Error != nil {
		t.Errorf("expected nil Error for successful ToolCall")
	}
	if tc.Duration != 200*time.Millisecond {
		t.Errorf("expected Duration 200ms, got %v", tc.Duration)
	}
}
