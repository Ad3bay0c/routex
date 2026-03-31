package routex

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Ad3bay0c/routex/agents"
	"github.com/Ad3bay0c/routex/llm"
	"github.com/Ad3bay0c/routex/tools"
)

func testLLMConfig(srv *httptest.Server) llm.Config {
	return llm.Config{
		Provider:    "openai",
		Model:       "gpt-4o",
		APIKey:      "test-key",
		BaseURL:     srv.URL,
		Timeout:     10 * time.Second,
		MaxTokens:   1024,
		Temperature: 0.7,
	}
}

func mockOpenAIServer(t *testing.T, content string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
			t.Errorf("mock response encode: %v", err)
		}
	}))
}

func minimalRuntimeConfig(srv *httptest.Server, ags []agents.Config) Config {
	return Config{
		Name:     "test-runtime",
		LogLevel: "error",
		LLM:      testLLMConfig(srv),
		Memory:   MemoryConfig{Backend: "inmem"},
		Agents:   ags,
		Task: Task{
			Input:       "hello",
			MaxDuration: 30 * time.Second,
		},
	}
}

func TestNewRuntime_Success(t *testing.T) {
	srv := mockOpenAIServer(t, "ok")
	defer srv.Close()

	rt, err := NewRuntime(minimalRuntimeConfig(srv, nil))
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	if rt == nil {
		t.Fatal("NewRuntime returned nil *Runtime")
	}
	rt.Stop()
}

func TestNewRuntime_InvalidMemoryBackend(t *testing.T) {
	srv := mockOpenAIServer(t, "ok")
	defer srv.Close()

	cfg := minimalRuntimeConfig(srv, nil)
	cfg.Memory.Backend = "unknown-backend"

	_, err := NewRuntime(cfg)
	if err == nil {
		t.Fatal("expected error for invalid memory backend")
	}
	if !strings.Contains(err.Error(), "memory") {
		t.Errorf("error = %v, want mention memory", err)
	}
}

func TestNewRuntime_InvalidLLMProvider(t *testing.T) {
	cfg := Config{
		Name:   "x",
		LLM:    llm.Config{Provider: "not-a-provider", Model: "m", APIKey: "k"},
		Memory: MemoryConfig{Backend: "inmem"},
	}
	_, err := NewRuntime(cfg)
	if err == nil {
		t.Fatal("expected error for invalid LLM provider")
	}
}

func TestRuntime_SetTask_GetTask_SetLogLevel(t *testing.T) {
	srv := mockOpenAIServer(t, "ok")
	defer srv.Close()

	rt, err := NewRuntime(minimalRuntimeConfig(srv, nil))
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	defer rt.Stop()

	rt.SetTask(Task{Input: "override", OutputFile: "/tmp/out.md"})
	got := rt.GetTask()
	if got.Input != "override" || got.OutputFile != "/tmp/out.md" {
		t.Fatalf("GetTask = %+v", got)
	}

	rt.SetLogLevel("debug")
	rt.SetLogLevel("warn")
}

func TestRuntime_RegisterTool(t *testing.T) {
	srv := mockOpenAIServer(t, "ok")
	defer srv.Close()

	rt, err := NewRuntime(minimalRuntimeConfig(srv, nil))
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	defer rt.Stop()

	rt.RegisterTool(&stubTool{name: "stub_tool"})
	if !rt.registry.Has("stub_tool") {
		t.Fatal("tool not registered")
	}
	if _, ok := rt.registry.Get("stub_tool"); !ok {
		t.Fatal("tool not found")
	}
	if len(rt.registry.List()) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(rt.registry.List()))
	}
	if len(rt.registry.Schemas()) != 1 {
		t.Fatalf("expected 1 schema, got %d", len(rt.registry.Schemas()))
	}
}

type stubTool struct{ name string }

func (s *stubTool) Name() string         { return s.name }
func (s *stubTool) Schema() tools.Schema { return tools.Schema{Description: "stub"} }
func (s *stubTool) Execute(context.Context, json.RawMessage) (json.RawMessage, error) {
	return json.RawMessage(`"ok"`), nil
}

func TestRuntime_AddAgent_InvalidLLMFallsBackToRuntimeDefault(t *testing.T) {
	srv := mockOpenAIServer(t, "fallback-ok")
	defer srv.Close()

	rt, err := NewRuntime(minimalRuntimeConfig(srv, nil))
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	defer rt.Stop()

	bad := "bogus-provider"
	rt.AddAgent(agents.Config{
		ID:   "a1",
		Role: agents.Researcher,
		Goal: "test",
		LLM:  &llm.Config{Provider: bad, Model: "m", APIKey: "k"},
	})
	if len(rt.agentList) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(rt.agentList))
	}
}

func TestRuntime_ExecutionPlan(t *testing.T) {
	srv := mockOpenAIServer(t, "ok")
	defer srv.Close()

	cfg := minimalRuntimeConfig(srv, []agents.Config{
		{ID: "r1", Role: agents.Researcher, Goal: "g1", Restart: agents.OneForOne},
		{ID: "w1", Role: agents.Writer, Goal: "g2", DependsOn: []string{"r1"}, Restart: agents.OneForOne},
	})
	cfg.Agents[1].LLM = &llm.Config{Provider: "openai", Model: "gpt-4o-mini", APIKey: "k"}

	rt, err := NewRuntime(cfg)
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	defer rt.Stop()

	plan := rt.ExecutionPlan()
	if len(plan) != 2 {
		t.Fatalf("expected 2 waves, got %d: %#v", len(plan), plan)
	}
	if len(plan[0]) != 1 || plan[0][0].ID != "r1" {
		t.Errorf("wave0 = %#v", plan[0])
	}
	if len(plan[1]) != 1 || plan[1][0].ID != "w1" {
		t.Errorf("wave1 = %#v", plan[1])
	}
	if plan[1][0].LLMProvider != "openai" || plan[1][0].LLMModel != "gpt-4o-mini" {
		t.Errorf("per-agent LLM in plan = %q / %q", plan[1][0].LLMProvider, plan[1][0].LLMModel)
	}
}

func TestRuntime_Start_NoAgents(t *testing.T) {
	srv := mockOpenAIServer(t, "ok")
	defer srv.Close()

	rt, err := NewRuntime(minimalRuntimeConfig(srv, nil))
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	defer rt.Stop()

	err = rt.Start(context.Background())
	if err == nil {
		t.Fatal("Start: want error with no agents")
	}
	if !strings.Contains(err.Error(), "no agents") {
		t.Errorf("error = %v", err)
	}
}

func TestRuntime_Start_AlreadyStarted(t *testing.T) {
	srv := mockOpenAIServer(t, "done")
	defer srv.Close()

	cfg := minimalRuntimeConfig(srv, []agents.Config{
		{ID: "solo", Role: agents.Researcher, Goal: "say hi", Restart: agents.OneForOne},
	})
	rt, err := NewRuntime(cfg)
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	defer rt.Stop()

	ctx := context.Background()
	if err := rt.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := rt.Start(ctx); err == nil {
		t.Fatal("second Start should error")
	}
}

func TestRuntime_Run_NotStarted(t *testing.T) {
	srv := mockOpenAIServer(t, "ok")
	defer srv.Close()

	rt, err := NewRuntime(minimalRuntimeConfig(srv, []agents.Config{
		{ID: "solo", Role: agents.Researcher, Goal: "g", Restart: agents.OneForOne},
	}))
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	defer rt.Stop()

	_, err = rt.Run(context.Background(), Task{Input: "x"})
	if err == nil || !strings.Contains(err.Error(), "Start()") {
		t.Fatalf("Run error = %v", err)
	}
}

func TestRuntime_Start_AutoRegisterMCPMissingURL(t *testing.T) {
	srv := mockOpenAIServer(t, "ok")
	defer srv.Close()

	cfg := minimalRuntimeConfig(srv, []agents.Config{
		{ID: "solo", Role: agents.Researcher, Goal: "g", Restart: agents.OneForOne},
	})
	cfg.ToolConfigs = []tools.ToolConfig{
		{Name: "mcp", Extra: map[string]string{}},
	}
	rt, err := NewRuntime(cfg)
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	defer rt.Stop()

	err = rt.Start(context.Background())
	if err == nil {
		t.Fatal("expected MCP config error")
	}
	if !strings.Contains(err.Error(), "server_url") {
		t.Errorf("error = %v", err)
	}
}

func TestRuntime_Integration_StartRunStop(t *testing.T) {
	const want = "integration reply"
	srv := mockOpenAIServer(t, want)
	defer srv.Close()

	cfg := minimalRuntimeConfig(srv, []agents.Config{
		{ID: "solo", Role: agents.Researcher, Goal: "Answer briefly", Restart: agents.OneForOne},
	})
	outDir := t.TempDir()
	outFile := filepath.Join(outDir, "out.md")
	cfg.Task.OutputFile = outFile

	rt, err := NewRuntime(cfg)
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}

	ctx := context.Background()
	if err = rt.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	result, err := rt.Run(ctx, rt.task)
	rt.Stop()

	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(result.Output, want) {
		t.Errorf("Output = %q, want substring %q", result.Output, want)
	}
	if result.TokensUsed <= 0 {
		t.Errorf("TokensUsed = %d, want > 0", result.TokensUsed)
	}
	body, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read output file: %v", err)
	}
	if !strings.Contains(string(body), want) {
		t.Errorf("file content = %q", body)
	}
}

func TestRuntime_StartAndRun(t *testing.T) {
	const want = "start and run"
	srv := mockOpenAIServer(t, want)
	defer srv.Close()

	cfg := minimalRuntimeConfig(srv, []agents.Config{
		{ID: "solo", Role: agents.Researcher, Goal: "g", Restart: agents.OneForOne},
	})
	rt, err := NewRuntime(cfg)
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}

	result, err := rt.StartAndRun(context.Background())
	rt.Stop()

	if err != nil {
		t.Fatalf("StartAndRun: %v", err)
	}
	if !strings.Contains(result.Output, want) {
		t.Errorf("Output = %q", result.Output)
	}
}
