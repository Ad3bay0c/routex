package scheduler

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Ad3bay0c/routex/agents"
	"github.com/Ad3bay0c/routex/internal/supervisor"
	"github.com/Ad3bay0c/routex/llm"
	"github.com/Ad3bay0c/routex/memory"
	"github.com/Ad3bay0c/routex/tools"

	"log/slog"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// schedMockAdapter is a concurrency-safe LLM stub for scheduler integration tests.
type schedMockAdapter struct {
	mu        sync.Mutex
	responses []llm.Response
	errors    []error
	calls     int
}

func (m *schedMockAdapter) Complete(_ context.Context, _ llm.Request) (llm.Response, error) {
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
	return llm.Response{}, errors.New("schedMockAdapter: no more scripted responses")
}

func (m *schedMockAdapter) Model() string    { return "mock" }
func (m *schedMockAdapter) Provider() string { return "mock" }

func schedTextResponse(content string) llm.Response {
	return llm.Response{
		Content:      content,
		FinishReason: "end_turn",
		Usage:        llm.TokenUsage{InputTokens: 3, OutputTokens: 5},
	}
}

func newSchedAgent(t *testing.T, id string, dependsOn []string, adapter llm.Adapter) *agents.Agent {
	t.Helper()
	mem := memory.NewInMemStore()
	t.Cleanup(func() { _ = mem.Close() })
	cfg := agents.Config{
		ID:         id,
		Role:       agents.Researcher,
		Goal:       "test goal",
		DependsOn:  dependsOn,
		MaxRetries: 0,
		Restart:    agents.OneForOne,
		Timeout:    30 * time.Second,
	}
	return agents.New(cfg, adapter, mem, tools.NewRegistry(), discardLogger(), nil, nil)
}

// drainAgentNotify runs until stop is closed so agents never block on notify sends
// (the scheduler only reads Output; production supervisor reads Notify).
func drainAgentNotify(stop <-chan struct{}, agentList []*agents.Agent) {
	var wg sync.WaitGroup
	for _, a := range agentList {
		wg.Add(1)
		go func(agent *agents.Agent) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				case <-agent.Notify():
				}
			}
		}(a)
	}
	wg.Wait()
}

func startSupervisorAndScheduler(t *testing.T, agentList []*agents.Agent) (*Scheduler, context.CancelFunc) {
	t.Helper()
	logger := discardLogger()
	sup := supervisor.New(agentList, agents.OneForOne, 3, time.Minute, logger)
	ctx, cancel := context.WithCancel(context.Background())
	sup.Start(ctx)
	stopDrain := make(chan struct{})
	var drainWG sync.WaitGroup
	drainWG.Add(1)
	go func() {
		defer drainWG.Done()
		drainAgentNotify(stopDrain, agentList)
	}()
	sch := New(agentList, sup, logger)
	t.Cleanup(func() {
		close(stopDrain)
		drainWG.Wait()
		cancel()
		sup.Stop()
	})
	return sch, cancel
}

func TestRunIDContext(t *testing.T) {
	ctx := context.Background()
	if runIDFromContext(ctx) != "unknown-run" {
		t.Errorf("default run id = %q", runIDFromContext(ctx))
	}
	ctx = WithRunID(ctx, "abc-123")
	if runIDFromContext(ctx) != "abc-123" {
		t.Errorf("run id = %q, want abc-123", runIDFromContext(ctx))
	}
}

func TestResolveInput(t *testing.T) {
	s := &Scheduler{logger: discardLogger()}

	ad := &schedMockAdapter{responses: []llm.Response{schedTextResponse("x")}}
	root := newSchedAgent(t, "root", nil, ad)
	child := newSchedAgent(t, "child", []string{"root"}, ad)
	two := newSchedAgent(t, "merge", []string{"a", "b"}, ad)

	t.Run("no_dependencies_uses_default", func(t *testing.T) {
		got := s.resolveInput(root, "default-in", nil)
		if got != "default-in" {
			t.Errorf("got %q, want default-in", got)
		}
	})

	t.Run("single_dependency_uses_output", func(t *testing.T) {
		prev := map[string]agents.Result{"root": {Output: "upstream"}}
		got := s.resolveInput(child, "default-in", prev)
		if got != "upstream" {
			t.Errorf("got %q, want upstream", got)
		}
	})

	t.Run("single_dependency_missing_falls_back", func(t *testing.T) {
		got := s.resolveInput(child, "fallback", map[string]agents.Result{})
		if got != "fallback" {
			t.Errorf("got %q, want fallback", got)
		}
	})

	t.Run("multiple_dependencies_combined", func(t *testing.T) {
		prev := map[string]agents.Result{
			"a": {Output: "AAA"},
			"b": {Output: "BBB"},
		}
		got := s.resolveInput(two, "unused", prev)
		if !strings.Contains(got, "=== Output from a ===") || !strings.Contains(got, "AAA") {
			t.Errorf("combined a missing: %q", got)
		}
		if !strings.Contains(got, "=== Output from b ===") || !strings.Contains(got, "BBB") {
			t.Errorf("combined b missing: %q", got)
		}
	})

	t.Run("multiple_dependencies_empty_falls_back", func(t *testing.T) {
		got := s.resolveInput(two, "fb", map[string]agents.Result{"a": {}, "b": {}})
		if got != "fb" {
			t.Errorf("got %q, want fb", got)
		}
	})
}

func TestScheduler_Run_SingleAgent_Success(t *testing.T) {
	ad := &schedMockAdapter{responses: []llm.Response{schedTextResponse("final answer")}}
	ag := newSchedAgent(t, "solo", nil, ad)
	sch, _ := startSupervisorAndScheduler(t, []*agents.Agent{ag})

	ctx := WithRunID(context.Background(), "run-test-1")
	results, err := sch.Run(ctx, "hello crew")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	r, ok := results["solo"]
	if !ok {
		t.Fatalf("missing solo result: %v", results)
	}
	if r.Err != nil {
		t.Fatalf("agent error: %v", r.Err)
	}
	if r.Output != "final answer" {
		t.Errorf("Output = %q", r.Output)
	}
}

func TestScheduler_Run_TwoAgentChain_PassesOutput(t *testing.T) {
	ad1 := &schedMockAdapter{responses: []llm.Response{schedTextResponse("planner-out")}}
	ad2 := &schedMockAdapter{responses: []llm.Response{schedTextResponse("writer-done")}}
	p := newSchedAgent(t, "planner", nil, ad1)
	w := newSchedAgent(t, "writer", []string{"planner"}, ad2)
	sch, _ := startSupervisorAndScheduler(t, []*agents.Agent{p, w})

	results, err := sch.Run(context.Background(), "topic")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if results["planner"].Output != "planner-out" {
		t.Errorf("planner output = %q", results["planner"].Output)
	}
	if results["writer"].Output != "writer-done" {
		t.Errorf("writer output = %q", results["writer"].Output)
	}
	// writer's LLM should have been invoked with planner output in history/user path —
	// we at least know the run succeeded with two waves.
	if ad2.calls != 1 {
		t.Errorf("writer adapter calls = %d, want 1", ad2.calls)
	}
}

func TestScheduler_Run_TwoRootsParallel(t *testing.T) {
	ad1 := &schedMockAdapter{responses: []llm.Response{schedTextResponse("A")}}
	ad2 := &schedMockAdapter{responses: []llm.Response{schedTextResponse("B")}}
	a := newSchedAgent(t, "left", nil, ad1)
	b := newSchedAgent(t, "right", nil, ad2)
	sch, _ := startSupervisorAndScheduler(t, []*agents.Agent{a, b})

	_, err := sch.Run(context.Background(), "parallel")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if ad1.calls != 1 || ad2.calls != 1 {
		t.Errorf("calls left=%d right=%d", ad1.calls, ad2.calls)
	}
}

func TestScheduler_Run_InvalidGraph_MissingDependency(t *testing.T) {
	ad := &schedMockAdapter{}
	orphan := newSchedAgent(t, "writer", []string{"missing-planner"}, ad)
	sch, _ := startSupervisorAndScheduler(t, []*agents.Agent{orphan})

	_, err := sch.Run(context.Background(), "x")
	if err == nil {
		t.Fatal("expected error for missing depends_on target")
	}
	if !strings.Contains(err.Error(), "invalid dependency graph") {
		t.Errorf("error = %v", err)
	}
}

func TestScheduler_Run_InvalidGraph_Cycle(t *testing.T) {
	ad := &schedMockAdapter{}
	x := newSchedAgent(t, "x", []string{"y"}, ad)
	y := newSchedAgent(t, "y", []string{"x"}, ad)
	sch, _ := startSupervisorAndScheduler(t, []*agents.Agent{x, y})

	_, err := sch.Run(context.Background(), "x")
	if err == nil {
		t.Fatal("expected error for cycle")
	}
	if !strings.Contains(err.Error(), "invalid dependency graph") {
		t.Errorf("error = %v", err)
	}
}

func TestScheduler_Run_SupervisorRetryThenSuccess(t *testing.T) {
	// Call 0 → error; call 1 → success (responses indexed by Complete call count).
	ad := &schedMockAdapter{
		errors: []error{errors.New("transient llm")},
		responses: []llm.Response{
			{}, // unused slot for index 0 (error short-circuits before response)
			schedTextResponse("recovered"),
		},
	}
	ag := newSchedAgent(t, "flaky", nil, ad)
	sch, _ := startSupervisorAndScheduler(t, []*agents.Agent{ag})

	results, err := sch.Run(context.Background(), "try")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if results["flaky"].Output != "recovered" {
		t.Errorf("Output = %q", results["flaky"].Output)
	}
	if ad.calls != 2 {
		t.Errorf("adapter calls = %d, want 2 (fail + retry)", ad.calls)
	}
}

func TestScheduler_Run_ContextCancelled(t *testing.T) {
	ad := &schedMockAdapter{}
	ag := newSchedAgent(t, "slow", nil, ad)
	logger := discardLogger()
	sup := supervisor.New([]*agents.Agent{ag}, agents.OneForOne, 3, time.Minute, logger)
	ctx, cancel := context.WithCancel(context.Background())
	sup.Start(ctx)
	stopDrain := make(chan struct{})
	var drainWG sync.WaitGroup
	drainWG.Add(1)
	go func() {
		defer drainWG.Done()
		drainAgentNotify(stopDrain, []*agents.Agent{ag})
	}()
	sch := New([]*agents.Agent{ag}, sup, logger)
	t.Cleanup(func() {
		close(stopDrain)
		drainWG.Wait()
		cancel()
		sup.Stop()
	})

	runCtx, runCancel := context.WithCancel(context.Background())
	runCancel()
	_, err := sch.Run(runCtx, "work")
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want Canceled", err)
	}
}
