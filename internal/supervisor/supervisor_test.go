package supervisor

import (
	"context"
	"fmt"
	"log/slog"
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

type noopAdapter struct{}

func (a *noopAdapter) Complete(ctx context.Context, _ llm.Request) (llm.Response, error) {
	// Block until context is cancelled — simulates an agent waiting for work.
	<-ctx.Done()
	return llm.Response{}, ctx.Err()
}
func (a *noopAdapter) Model() string    { return "noop" }
func (a *noopAdapter) Provider() string { return "mock" }

func newTestAgent(t *testing.T, id string) *agents.Agent {
	t.Helper()
	mem := memory.NewInMemStore()
	t.Cleanup(func() { mem.Close() })

	logger := slog.New(slog.NewTextHandler(testLogWriter{t}, nil))
	cfg := agents.Config{
		ID:      id,
		Role:    agents.Researcher,
		Goal:    "test",
		Timeout: 5 * time.Second,
	}
	return agents.New(cfg, &noopAdapter{}, mem, tools.NewRegistry(), logger, nil, nil)
}

func newTestSupervisor(t *testing.T, agentList []*agents.Agent) (*Supervisor, context.CancelFunc) {
	t.Helper()
	return newTestSupervisorWithLimits(t, agentList, 3, time.Minute)
}

func newTestSupervisorWithLimits(
	t *testing.T,
	agentList []*agents.Agent,
	maxRestarts int,
	window time.Duration,
) (*Supervisor, context.CancelFunc) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(testLogWriter{t}, nil))
	sup := New(agentList, agents.OneForOne, maxRestarts, window, logger)

	ctx, cancel := context.WithCancel(context.Background())
	sup.Start(ctx)

	t.Cleanup(func() {
		cancel()
		sup.Stop()
	})

	return sup, cancel
}

func failureReport(agentID string, err error) (FailureReport, chan Decision) {
	reply := make(chan Decision, 1)
	return FailureReport{AgentID: agentID, Err: err, Reply: reply}, reply
}

func TestSupervisor_RestartsFailedAgent(t *testing.T) {
	ag := newTestAgent(t, "test-agent")
	sup, _ := newTestSupervisor(t, []*agents.Agent{ag})

	// Give goroutines time to start
	time.Sleep(30 * time.Millisecond)

	report, replyCh := failureReport("test-agent", fmt.Errorf("llm timeout"))
	sup.FailureReports <- report

	select {
	case decision := <-replyCh:
		if !decision.Retry {
			t.Errorf("Retry = false, want true")
		}
		if decision.Err != nil {
			t.Errorf("Err = %v, want nil", decision.Err)
		}
		if decision.AgentID != "test-agent" {
			t.Errorf("AgentID = %q, want %q", decision.AgentID, "test-agent")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for supervisor decision")
	}
}

func TestSupervisor_ExhaustsRestartBudget(t *testing.T) {
	ag := newTestAgent(t, "test-agent")
	// maxRestarts=2 → budget exhausted after 2 restarts
	sup, _ := newTestSupervisorWithLimits(t, []*agents.Agent{ag}, 2, time.Minute)
	time.Sleep(30 * time.Millisecond)

	// Consume the allowed restarts
	for i := 0; i < 2; i++ {
		report, replyCh := failureReport("test-agent", fmt.Errorf("fail %d", i))
		sup.FailureReports <- report
		<-replyCh
	}

	// Third failure — budget gone
	report, replyCh := failureReport("test-agent", fmt.Errorf("fail 3"))
	sup.FailureReports <- report

	select {
	case decision := <-replyCh:
		if decision.Retry {
			t.Error("Retry = true after budget exhausted, want false")
		}
		if decision.Err == nil {
			t.Error("Err should be non-nil when budget exhausted")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for permanent failure decision")
	}
}

func TestSupervisor_RestartWindowSlides(t *testing.T) {
	ag := newTestAgent(t, "test-agent")
	// 1 restart within a 100ms window
	sup, _ := newTestSupervisorWithLimits(t, []*agents.Agent{ag}, 1, 100*time.Millisecond)
	time.Sleep(30 * time.Millisecond)

	// First failure — should retry (1 within window)
	r1, rch1 := failureReport("test-agent", fmt.Errorf("fail 1"))
	sup.FailureReports <- r1
	d1 := <-rch1
	if !d1.Retry {
		t.Fatal("first failure should retry")
	}

	// Wait for the window to expire
	time.Sleep(120 * time.Millisecond)

	// Second failure — fresh window, should retry again
	r2, rch2 := failureReport("test-agent", fmt.Errorf("fail 2"))
	sup.FailureReports <- r2
	d2 := <-rch2
	if !d2.Retry {
		t.Error("second failure after window expiry should retry (window reset)")
	}
}

func TestSupervisor_MultipleAgentsIndependent(t *testing.T) {
	ag1 := newTestAgent(t, "agent-1")
	ag2 := newTestAgent(t, "agent-2")

	sup, _ := newTestSupervisor(t, []*agents.Agent{ag1, ag2})
	time.Sleep(30 * time.Millisecond)

	// Fail only agent-1
	report, replyCh := failureReport("agent-1", fmt.Errorf("fail"))
	sup.FailureReports <- report

	decision := <-replyCh
	if !decision.Retry {
		t.Error("agent-1 should retry")
	}

	// ag2's inbox should still be open (not restarted / not blocked)
	select {
	case ag2.Inbox <- agents.Message{RunID: "r", Input: "ping"}:
		// good
	default:
		t.Error("ag2 inbox full — was it incorrectly restarted?")
	}
}

func TestSupervisor_StartsAllAgents(t *testing.T) {
	ag1 := newTestAgent(t, "agent-1")
	ag2 := newTestAgent(t, "agent-2")

	_, _ = newTestSupervisor(t, []*agents.Agent{ag1, ag2})
	time.Sleep(50 * time.Millisecond)

	// Both agents' inboxes should be live
	for _, ag := range []*agents.Agent{ag1, ag2} {
		select {
		case ag.Inbox <- agents.Message{RunID: "r", Input: "ping"}:
		default:
			t.Errorf("agent %s inbox blocked — goroutine may not have started", ag.ID())
		}
	}
}
