package supervisor

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/Ad3bay0c/routex/agents"
	"github.com/Ad3bay0c/routex/llm"
	"github.com/Ad3bay0c/routex/memory"
	"github.com/Ad3bay0c/routex/tools"
)

// supervisorDecision aliases supervisor.Decision for cleaner test code.
type supervisorDecision = Decision

type testLogWriter struct{ t *testing.T }

func (w testLogWriter) Write(p []byte) (int, error) {
	w.t.Log(string(p))
	return len(p), nil
}

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

// supervisorFailureReport builds a FailureReport.
func supervisorFailureReport(agentID string, err error, reply chan Decision) FailureReport {
	return FailureReport{
		AgentID: agentID,
		Err:     err,
		Reply:   reply,
	}
}

// newTestSupervisor creates a Supervisor with default limits for testing.
func newTestSupervisor(t *testing.T, agentList []*agents.Agent) *Supervisor {
	t.Helper()
	return newTestSupervisorWithLimits(t, agentList, 3, time.Minute)
}

// newTestSupervisorWithLimits creates a Supervisor with custom restart limits.
func newTestSupervisorWithLimits(
	t *testing.T,
	agentList []*agents.Agent,
	maxRestarts int,
	window time.Duration,
) *Supervisor {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(testLogWriter{t}, nil))
	return New(agentList, agents.OneForOne, maxRestarts, window, logger)
}

func newTestAgent(t *testing.T, adapter llm.Adapter, toolList ...tools.Tool) *agents.Agent {
	t.Helper()

	mem := memory.NewInMemStore()
	t.Cleanup(func() { mem.Close() })

	reg := tools.NewRegistry()
	for _, tool := range toolList {
		reg.Register(tool)
	}

	cfg := agents.Config{
		ID:         "test-agent",
		Role:       agents.Researcher,
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

	return agents.New(cfg, adapter, mem, reg, logger, nil, nil)
}

func newAgentConfig(id string) agents.Config {
	return agents.Config{
		ID:         id,
		Role:       agents.Researcher,
		Goal:       "complete the test task",
		MaxRetries: 0,
		Timeout:    5 * time.Second,
	}
}

func TestSupervisor_RestartsFailedAgent(t *testing.T) {
	ag := newTestAgent(t, &mockAdapter{})
	sup := newTestSupervisor(t, []*agents.Agent{ag})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sup.Start(ctx)
	time.Sleep(30 * time.Millisecond) // let goroutines start

	// Report a failure
	replyCh := make(chan supervisorDecision, 1)
	sup.FailureReports <- supervisorFailureReport(ag.ID(), fmt.Errorf("llm timeout"), replyCh)

	select {
	case decision := <-replyCh:
		if !decision.Retry {
			t.Errorf("Retry = false, want true")
		}
		if decision.Err != nil {
			t.Errorf("Err = %v, want nil", decision.Err)
		}
		if decision.AgentID != ag.ID() {
			t.Errorf("AgentID = %q, want %q", decision.AgentID, ag.ID())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for supervisor decision")
	}
}

func TestSupervisor_ExhaustsRestartBudget(t *testing.T) {
	ag := newTestAgent(t, &mockAdapter{})

	// maxRestarts=2 → budget exhausted after 2 restarts
	sup := newTestSupervisorWithLimits(t, []*agents.Agent{ag}, 2, time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sup.Start(ctx)
	time.Sleep(30 * time.Millisecond)

	// Consume both allowed restarts
	for i := 0; i < 2; i++ {
		r := make(chan supervisorDecision, 1)
		sup.FailureReports <- supervisorFailureReport(ag.ID(), fmt.Errorf("fail %d", i), r)
		<-r
	}

	// Third failure — budget exhausted
	r := make(chan supervisorDecision, 1)
	sup.FailureReports <- supervisorFailureReport(ag.ID(), fmt.Errorf("fail 3"), r)

	select {
	case decision := <-r:
		if decision.Retry {
			t.Error("Retry = true, want false (budget exhausted)")
		}
		if decision.Err == nil {
			t.Error("Err should be non-nil when budget exhausted")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for permanent failure decision")
	}
}

func TestSupervisor_RestartWindowSlides(t *testing.T) {
	ag := newTestAgent(t, &mockAdapter{})

	// 1 restart within a 100ms window
	sup := newTestSupervisorWithLimits(t, []*agents.Agent{ag}, 1, 100*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sup.Start(ctx)
	time.Sleep(30 * time.Millisecond)

	// First failure — should retry (1 within window)
	r1 := make(chan supervisorDecision, 1)
	sup.FailureReports <- supervisorFailureReport(ag.ID(), fmt.Errorf("fail 1"), r1)
	d1 := <-r1
	if !d1.Retry {
		t.Fatal("first failure should retry")
	}

	// Wait for the window to expire
	time.Sleep(120 * time.Millisecond)

	// Second failure after window — fresh window, should retry again
	r2 := make(chan supervisorDecision, 1)
	sup.FailureReports <- supervisorFailureReport(ag.ID(), fmt.Errorf("fail 2"), r2)
	d2 := <-r2
	if !d2.Retry {
		t.Error("second failure after window expiry should retry (window reset)")
	}
}

func TestSupervisor_MultipleAgentsIndependent(t *testing.T) {
	// With OneForOne policy, failing agent-1 should not affect agent-2
	agent1 := newTestAgent(t, &mockAdapter{})
	agent1.SetConfig(newAgentConfig("agent-1"))

	agent2 := newTestAgent(t, &mockAdapter{})
	agent2.SetConfig(newAgentConfig("agent-2"))

	sup := newTestSupervisor(t, []*agents.Agent{agent1, agent2})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sup.Start(ctx)
	time.Sleep(30 * time.Millisecond)

	// Only report agent1 as failed
	r := make(chan supervisorDecision, 1)
	sup.FailureReports <- supervisorFailureReport("agent-1", fmt.Errorf("fail"), r)

	decision := <-r
	if !decision.Retry {
		t.Error("agent-1 should retry")
	}

	// agent2's inbox should still be accepting messages (not restarted)
	select {
	case agent2.Inbox <- agents.Message{RunID: "r", Input: "ping"}:
		// good — agent2 goroutine is alive and inbox not blocked
	default:
		t.Error("ag2 inbox is full — was it incorrectly restarted?")
	}
}
