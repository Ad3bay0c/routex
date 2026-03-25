package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/Ad3bay0c/routex/llm"
	"github.com/Ad3bay0c/routex/memory"
	"github.com/Ad3bay0c/routex/tools"
)

// blockingAdapter blocks until its context is cancelled.
// Used to simulate a stuck LLM call.
type blockingAdapter struct {
	block chan struct{}
}

func (b *blockingAdapter) Complete(ctx context.Context, _ llm.Request) (llm.Response, error) {
	select {
	case <-b.block:
		return llm.Response{}, nil
	case <-ctx.Done():
		return llm.Response{}, ctx.Err()
	}
}
func (b *blockingAdapter) Model() string    { return "blocking_model" }
func (b *blockingAdapter) Provider() string { return "mock_provider" }

// mockAdapter mocks LLM adapter.
type mockAdapter struct {
	mu        sync.Mutex
	responses []llm.Response
	errors    []error
	calls     int
}

func (m *mockAdapter) Complete(_ context.Context, _ llm.Request) (llm.Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	i := m.calls
	m.calls++
	if i < len(m.errors) && m.errors[i] != nil {
		return llm.Response{}, m.errors[i]
	}
	if i < len(m.responses) {
		return m.responses[i], nil
	}
	return llm.Response{}, nil
}
func (m *mockAdapter) Model() string    { return "mock-model" }
func (m *mockAdapter) Provider() string { return "mock-provider" }

// textResponse returns an LLM response with plain text content.
func textResponse(content string) llm.Response {
	return llm.Response{
		Content:      content,
		FinishReason: "end_turn",
		Usage:        llm.TokenUsage{InputTokens: 10, OutputTokens: 20},
	}
}

// toolCallResponse returns an LLM response requesting a tool call.
func toolCallResponse(toolName, input string) llm.Response {
	return llm.Response{
		ToolCall: &llm.ToolCallRequest{
			ID:       "tc_" + toolName,
			ToolName: toolName,
			Input:    input,
		},
		FinishReason: "tool_use",
		Usage:        llm.TokenUsage{InputTokens: 10, OutputTokens: 5},
	}
}

// mockTool mocks tool calls.
type mockTool struct {
	name   string
	output string
	err    error
	calls  int
	mu     sync.Mutex
}

func (t *mockTool) Name() string         { return t.name }
func (t *mockTool) Schema() tools.Schema { return tools.Schema{Description: "mock"} }
func (t *mockTool) Execute(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.calls++
	if t.err != nil {
		return nil, t.err
	}
	return json.RawMessage(`"` + t.output + `"`), nil
}

func newTestAgent(t *testing.T, adapter llm.Adapter, toolList ...tools.Tool) *Agent {
	t.Helper()

	mem := memory.NewInMemStore()
	t.Cleanup(func() { mem.Close() })

	reg := tools.NewRegistry()
	for _, tool := range toolList {
		reg.Register(tool)
	}

	cfg := Config{
		ID:         "test-agent",
		Role:       Researcher,
		Goal:       "complete the test task",
		MaxRetries: 0,
		Timeout:    5 * time.Second,
	}
	if len(toolList) > 0 {
		names := make([]string, len(toolList))
		for i, tool := range toolList {
			names[i] = tool.Name()
		}
		cfg.Tools = names
	}

	logger := slog.New(slog.NewTextHandler(testLogWriter{t}, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	return New(cfg, adapter, mem, reg, logger, nil, nil)
}

type testLogWriter struct{ t *testing.T }

func (w testLogWriter) Write(p []byte) (int, error) {
	w.t.Log(string(p))
	return len(p), nil
}

func TestAgent_SuccessOnFirstAttempt(t *testing.T) {
	adapter := &mockAdapter{
		responses: []llm.Response{textResponse("the answer is 42")},
	}
	ag := newTestAgent(t, adapter)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go ag.Run(ctx)

	ag.Inbox <- Message{RunID: "run-1", Input: "what is 6 times 7?"}
	result := <-ag.Output()

	if result.Err != nil {
		t.Fatalf("expected no error, got: %v", result.Err)
	}
	if result.Output != "the answer is 42" {
		t.Errorf("Output = %q, want %q", result.Output, "the answer is 42")
	}
	if result.AgentID != "test-agent" {
		t.Errorf("AgentID = %q, want %q", result.AgentID, "test-agent")
	}
	if result.TokensUsed != 30 {
		t.Errorf("TokensUsed = %d, want 30", result.TokensUsed)
	}
}

func TestAgent_BothChannelsReceiveResult(t *testing.T) {
	adapter := &mockAdapter{
		responses: []llm.Response{textResponse("done")},
	}
	ag := newTestAgent(t, adapter)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go ag.Run(ctx)
	ag.Inbox <- Message{RunID: "run-1", Input: "do something"}

	var outputResult, notifyResult Result
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		select {
		case r := <-ag.Output():
			outputResult = r
		case <-time.After(3 * time.Second):
			t.Error("timeout waiting for output channel")
		}
	}()

	go func() {
		defer wg.Done()
		select {
		case r := <-ag.Notify():
			notifyResult = r
		case <-time.After(3 * time.Second):
			t.Error("timeout waiting for notify channel")
		}
	}()

	wg.Wait()

	if outputResult.Output != "done" {
		t.Errorf("output channel: got %q, want %q", outputResult.Output, "done")
	}
	if notifyResult.Output != "done" {
		t.Errorf("notify channel: got %q, want %q", notifyResult.Output, "done")
	}
}

func TestAgent_ToolCallThenTextResponse(t *testing.T) {
	tool := &mockTool{name: "search", output: "sunny in Lagos"}

	adapter := &mockAdapter{
		responses: []llm.Response{
			toolCallResponse("search", `{"query":"Lagos weather"}`),
			textResponse("Lagos is sunny today"),
		},
	}

	ag := newTestAgent(t, adapter, tool)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go ag.Run(ctx)
	ag.Inbox <- Message{RunID: "run-1", Input: "what is the weather in Lagos?"}

	result := <-ag.Output()

	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}
	if result.Output != "Lagos is sunny today" {
		t.Errorf("Output = %q, want %q", result.Output, "Lagos is sunny today")
	}
	if len(result.ToolCalls) != 1 {
		t.Fatalf("ToolCalls len = %d, want 1", len(result.ToolCalls))
	}
	if result.ToolCalls[0].ToolName != "search" {
		t.Errorf("ToolCalls[0].ToolName = %q, want %q", result.ToolCalls[0].ToolName, "search")
	}
	if tool.calls != 1 {
		t.Errorf("tool.calls = %d, want 1", tool.calls)
	}
}

func TestAgent_ToolFailureReportedInResult(t *testing.T) {
	// Tool returns an error — agent should include it in the tool call log
	// and continue the LLM loop (so the LLM can decide what to do)
	tool := &mockTool{name: "search", err: fmt.Errorf("API rate limit exceeded")}

	adapter := &mockAdapter{
		responses: []llm.Response{
			toolCallResponse("search", `{"query":"test"}`),
			textResponse("search failed but here is what I know"),
		},
	}

	ag := newTestAgent(t, adapter, tool)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go ag.Run(ctx)
	ag.Inbox <- Message{RunID: "run-1", Input: "search for something"}

	result := <-ag.Output()

	if result.Err != nil {
		t.Fatalf("agent-level error unexpected: %v", result.Err)
	}
	if len(result.ToolCalls) != 1 {
		t.Fatalf("ToolCalls len = %d, want 1", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Error == nil {
		t.Error("ToolCalls[0].Error should be non-nil for failed tool")
	}
}

func TestAgent_DuplicateToolCallRedirect(t *testing.T) {
	tool := &mockTool{name: "search", output: "some result"}

	// LLM requests the same tool+input 3 times
	// With maxDuplicateCalls=2, the 3rd should be redirected (not executed)
	adapter := &mockAdapter{
		responses: []llm.Response{
			toolCallResponse("search", `{"query":"same"}`), // 1 — executes
			toolCallResponse("search", `{"query":"same"}`), // 2 — executes
			toolCallResponse("search", `{"query":"same"}`), // 3 — redirected
			textResponse("I have the results already"),     // LLM answers after redirect
		},
	}

	ag := newTestAgent(t, adapter, tool)
	ag.cfg.MaxDuplicateToolCalls = 2

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go ag.Run(ctx)
	ag.Inbox <- Message{RunID: "run-1", Input: "search for something"}

	result := <-ag.Output()

	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}
	if tool.calls != 2 {
		t.Errorf("tool.calls = %d, want 2 (3rd call should be redirected)", tool.calls)
	}
}

func TestAgent_TotalToolBudgetRedirect(t *testing.T) {
	tool := &mockTool{name: "search", output: "result"}

	// 20 calls with unique queries (no duplicates), then one more, then text
	responses := make([]llm.Response, 0, 22)
	for i := 0; i < 20; i++ {
		responses = append(responses, toolCallResponse("search",
			fmt.Sprintf(`{"query":"q%d"}`, i),
		))
	}
	responses = append(responses, toolCallResponse("search", `{"query":"q20"}`)) // 21st — over budget
	responses = append(responses, textResponse("done"))

	adapter := &mockAdapter{responses: responses}
	ag := newTestAgent(t, adapter, tool)
	ag.cfg.MaxTotalToolCalls = 20

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go ag.Run(ctx)
	ag.Inbox <- Message{RunID: "run-1", Input: "search many things"}

	result := <-ag.Output()

	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}
	if tool.calls != 20 {
		t.Errorf("tool.calls = %d, want exactly 20 (21st redirected)", tool.calls)
	}
}

func TestAgent_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	ag := newTestAgent(t, &blockingAdapter{block: make(chan struct{})})
	go ag.Run(ctx)
	ag.Inbox <- Message{RunID: "run-1", Input: "hello"}

	result := <-ag.Output()
	if result.Err == nil {
		t.Error("expected context cancellation error, got nil")
	}
}

func TestAgent_MultipleSequentialTasks(t *testing.T) {
	// Agent should handle multiple tasks sequentially after each other
	adapter := &mockAdapter{
		responses: []llm.Response{
			textResponse("answer to first"),
			textResponse("answer to second"),
		},
	}
	ag := newTestAgent(t, adapter)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go ag.Run(ctx)

	// First task
	ag.Inbox <- Message{RunID: "run-1", Input: "first question"}
	r1 := <-ag.Output()
	<-ag.Notify() // drain notify too
	if r1.Output != "answer to first" {
		t.Errorf("first task Output = %q, want %q", r1.Output, "answer to first")
	}

	// Second task — agent should be back waiting on Inbox
	ag.Inbox <- Message{RunID: "run-2", Input: "second question"}
	r2 := <-ag.Output()
	<-ag.Notify()
	if r2.Output != "answer to second" {
		t.Errorf("second task Output = %q, want %q", r2.Output, "answer to second")
	}
}
