package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Ad3bay0c/routex"
)

func mockOpenAIServerForRun(t *testing.T, content string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		err := json.NewEncoder(w).Encode(map[string]any{
			"id": "chatcmpl-test", "object": "chat.completion", "model": "gpt-4o",
			"choices": []map[string]any{{
				"index":         0,
				"message":       map[string]any{"role": "assistant", "content": content},
				"finish_reason": "stop",
			}},
			"usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 20},
		})
		if err != nil {
			t.Errorf("encode mock: %v", err)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func yamlRunOpenAI(baseURL string) string {
	return fmt.Sprintf(`
runtime:
  name:         "cli-run-test"
  llm_provider: "openai"
  model:        "gpt-4o"
  api_key:      "test-key"
  base_url:     %q

task:
  input:        "hello"
  max_duration: "5m"

agents:
  - id:      "solo"
    role:    "researcher"
    goal:    "Answer in one short sentence."
    restart: "one_for_one"

memory:
  backend: "inmem"

observability:
  tracing: false
  metrics: false
`, baseURL)
}

func yamlDryRunWithWaves() string {
	return `
runtime:
  name:         "dry"
  llm_provider: "anthropic"
  model:        "claude-haiku-4-5-20251001"
  api_key:      "sk-ant-test"

task:
  input:        "t"
  max_duration: "5m"

agents:
  - id:      "planner"
    role:    "planner"
    goal:    "plan things"
    restart: "one_for_one"
  - id:      "writer"
    role:    "writer"
    goal:    "write"
    restart: "one_for_one"
    depends_on: ["planner"]
    llm:
      provider: "anthropic"
      model:    "claude-sonnet-4-6"
      api_key:  "sk-ant-other"

memory:
  backend: "inmem"

observability:
  tracing: false
  metrics: false
`
}

func TestRunCommand_WrapsStdoutStderr(t *testing.T) {
	if err := runCommand([]string{}); err != nil {
		t.Fatalf("runCommand: %v", err)
	}
}

func TestRunCommandTo_PrintsUsageWhenNoPositional(t *testing.T) {
	var out, errBuf bytes.Buffer
	if err := runCommandTo(&out, &errBuf, []string{}); err != nil {
		t.Fatalf("runCommandTo: %v", err)
	}
	if !strings.Contains(out.String(), "routex run") {
		t.Errorf("stdout should contain usage, got: %q", out.String())
	}
}

func TestRunCommandTo_MissingConfigPath(t *testing.T) {
	var out, errBuf bytes.Buffer
	err := runCommandTo(&out, &errBuf, []string{"--dry-run"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(errBuf.String(), "agents.yaml path is required") {
		t.Errorf("stderr = %q", errBuf.String())
	}
}

func TestRunCommandTo_LoadConfigError(t *testing.T) {
	var out, errBuf bytes.Buffer
	p := filepath.Join(t.TempDir(), "missing.yaml")
	err := runCommandTo(&out, &errBuf, []string{p})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(errBuf.String(), "error:") {
		t.Errorf("stderr = %q", errBuf.String())
	}
}

func TestRunCommandTo_InvalidTimeout(t *testing.T) {
	srv := mockOpenAIServerForRun(t, "ok")
	path := writeTempConfig(t, yamlRunOpenAI(srv.URL))

	var out, errBuf bytes.Buffer
	err := runCommandTo(&out, &errBuf, []string{path, "--timeout", "not-a-duration"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(errBuf.String(), "invalid --timeout") {
		t.Errorf("stderr = %q", errBuf.String())
	}
}

func TestRunCommandTo_DryRun(t *testing.T) {
	path := writeTempConfig(t, yamlDryRunWithWaves())

	var out, errBuf bytes.Buffer
	if err := runCommandTo(&out, &errBuf, []string{path, "--dry-run"}); err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "dry-run") || !strings.Contains(s, "wave 1") || !strings.Contains(s, "wave 2") {
		t.Errorf("unexpected output: %q", s)
	}
	if !strings.Contains(s, "planner") || !strings.Contains(s, "writer") {
		t.Errorf("expected agent ids in plan: %q", s)
	}
	if !strings.Contains(s, "← planner") {
		t.Errorf("expected depends_on in output: %q", s)
	}
	if !strings.Contains(s, "[anthropic / claude-sonnet-4-6]") {
		t.Errorf("expected per-agent LLM note: %q", s)
	}
}

func TestRunCommandTo_TaskOutputLogLevelFlags(t *testing.T) {
	srv := mockOpenAIServerForRun(t, "done")
	path := writeTempConfig(t, yamlRunOpenAI(srv.URL))
	outFile := filepath.Join(t.TempDir(), "out.md")

	var out, errBuf bytes.Buffer
	err := runCommandTo(&out, &errBuf, []string{
		path,
		"-t", "custom task text",
		"-o", outFile,
		"-T", "10m",
		"-l", "error",
		"--json",
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	var jr struct {
		Success bool `json:"success"`
	}
	if err := json.Unmarshal(out.Bytes(), &jr); err != nil {
		t.Fatalf("stdout JSON: %v\n%s", err, out.String())
	}
	if !jr.Success {
		t.Errorf("success = false")
	}
}

func TestRunCommandTo_FullRunHuman(t *testing.T) {
	srv := mockOpenAIServerForRun(t, "human output ok")
	path := writeTempConfig(t, yamlRunOpenAI(srv.URL))

	var out, errBuf bytes.Buffer
	if err := runCommandTo(&out, &errBuf, []string{path}); err != nil {
		t.Fatalf("run: %v\nstderr: %s", err, errBuf.String())
	}
	if !strings.Contains(out.String(), "routex run") {
		t.Errorf("header missing: %q", out.String())
	}
	if !strings.Contains(out.String(), "tokens") {
		t.Errorf("expected summary: %q", out.String())
	}
}

func TestRunCommandTo_UnknownFlagReturnsError(t *testing.T) {
	srv := mockOpenAIServerForRun(t, "x")
	path := writeTempConfig(t, yamlRunOpenAI(srv.URL))

	err := runCommandTo(io.Discard, io.Discard, []string{path, "--not-a-real-flag"})
	if err == nil || !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("got err = %v", err)
	}
}

func TestPrintRunHeader_TruncatesLongTask(t *testing.T) {
	var buf bytes.Buffer
	long := strings.Repeat("a", 100)
	printRunHeader(&buf, "/cfg.yaml", routex.Task{
		Input:       long,
		OutputFile:  "/out.md",
		MaxDuration: 30 * time.Second,
	})
	s := buf.String()
	if !strings.Contains(s, "aaa...") {
		t.Errorf("truncation missing: %q", s)
	}
	if !strings.Contains(s, "output") || !strings.Contains(s, "timeout") {
		t.Errorf("expected output/timeout lines: %q", s)
	}
}

func TestPrintRunHeader_EmptyInput(t *testing.T) {
	var buf bytes.Buffer
	printRunHeader(&buf, "agents.yaml", routex.Task{})
	if strings.Contains(buf.String(), "task       ") {
		t.Errorf("should not print task line for empty input: %q", buf.String())
	}
}

func TestPrintResultHuman_RunFailedNoAgents(t *testing.T) {
	var out, errBuf bytes.Buffer
	runErr := errors.New("boom")
	err := printResultHuman(&out, &errBuf, routex.Result{}, runErr, time.Second)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(errBuf.String(), "run failed") {
		t.Errorf("stderr = %q", errBuf.String())
	}
}

func TestPrintResultHuman_PartialSuccessWithRunError(t *testing.T) {
	var out, errBuf bytes.Buffer
	runErr := errors.New("crew incomplete")
	res := routex.Result{
		TokensUsed: 42,
		AgentResults: map[string]routex.AgentResult{
			"a1": {TokensUsed: 10, ToolCalls: []routex.ToolCall{{ToolName: "t"}}},
			"a2": {TokensUsed: 32, Error: errors.New("agent failed")},
		},
	}
	err := printResultHuman(&out, &errBuf, res, runErr, 1500*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "crew incomplete") {
		t.Fatalf("err = %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "✓") || !strings.Contains(s, "✗") {
		t.Errorf("expected status markers: %q", s)
	}
	if !strings.Contains(s, "tokens  42") {
		t.Errorf("expected total tokens: %q", s)
	}
	if !strings.Contains(errBuf.String(), "completed with errors") {
		t.Errorf("stderr = %q", errBuf.String())
	}
}

func TestPrintResultHuman_SuccessWithTraceAndOutputFile(t *testing.T) {
	var out, errBuf bytes.Buffer
	res := routex.Result{
		TokensUsed: 7,
		TraceID:    "trace-xyz",
		OutputFile: "/tmp/report.md",
		AgentResults: map[string]routex.AgentResult{
			"solo": {TokensUsed: 7},
		},
	}
	if err := printResultHuman(&out, &errBuf, res, nil, time.Second); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if !strings.Contains(s, "trace-xyz") || !strings.Contains(s, "report.md") {
		t.Errorf("output: %q", s)
	}
}

func TestPrintResultJSON_Success(t *testing.T) {
	var out, errBuf bytes.Buffer
	res := routex.Result{
		TokensUsed: 5,
		AgentResults: map[string]routex.AgentResult{
			"x": {TokensUsed: 5, ToolCalls: []routex.ToolCall{{ToolName: "grep"}}},
		},
	}
	if err := printResultJSON(&out, &errBuf, res, nil); err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Success bool `json:"success"`
		Agents  []struct {
			ID        string `json:"id"`
			ToolCalls int    `json:"tool_calls"`
		} `json:"agents"`
	}
	if err := json.Unmarshal(out.Bytes(), &parsed); err != nil {
		t.Fatal(err)
	}
	if !parsed.Success || len(parsed.Agents) != 1 || parsed.Agents[0].ToolCalls != 1 {
		t.Fatalf("%+v", parsed)
	}
}

func TestPrintResultJSON_RunErrorStillPrintsJSON(t *testing.T) {
	var out, errBuf bytes.Buffer
	runErr := errors.New("json run err")
	res := routex.Result{
		TokensUsed: 1,
		AgentResults: map[string]routex.AgentResult{
			"a": {Error: errors.New("e1")},
		},
	}
	err := printResultJSON(&out, &errBuf, res, runErr)
	if err == nil || err.Error() != "json run err" {
		t.Fatalf("want runErr returned, got %v", err)
	}
	var parsed struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
		Agents  []struct {
			Error string `json:"error"`
		} `json:"agents"`
	}
	if e := json.Unmarshal(out.Bytes(), &parsed); e != nil {
		t.Fatal(e)
	}
	if parsed.Success || parsed.Error != "json run err" || parsed.Agents[0].Error != "e1" {
		t.Fatalf("%+v", parsed)
	}
}

func TestRunCommandTo_WithEnvFileAndTaskInput(t *testing.T) {
	srv := mockOpenAIServerForRun(t, "from env file")
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "agents.yaml")
	key := "TEST_ROUTEX_CMD_RUN_APIKEY"
	content := yamlRunOpenAI(srv.URL)
	content = strings.Replace(content, `api_key:      "test-key"`, fmt.Sprintf(`api_key:      "env:%s"`, key), 1)
	if err := os.WriteFile(cfgPath, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env.cli"), []byte(key+"=test-key\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Unsetenv(key) })
	_ = os.Unsetenv(key)

	var out, errBuf bytes.Buffer
	err := runCommandTo(&out, &errBuf, []string{
		cfgPath,
		"-e", ".env.cli",
		"-t", "task from flag",
		"--json",
	})
	if err != nil {
		t.Fatalf("%v\n%s", err, errBuf.String())
	}
	var jr struct {
		Success bool `json:"success"`
	}
	if err := json.Unmarshal(out.Bytes(), &jr); err != nil || !jr.Success {
		t.Fatalf("json out: %v / %s", err, out.String())
	}
}
