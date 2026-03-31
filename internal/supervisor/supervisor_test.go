package supervisor

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/Ad3bay0c/routex/agents"
	"github.com/Ad3bay0c/routex/llm"
	"github.com/Ad3bay0c/routex/memory"
	"github.com/Ad3bay0c/routex/tools"
)

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
	return newTestAgentWithDeps(t, id, nil)
}

func newTestAgentWithDeps(t *testing.T, id string, dependsOn []string) *agents.Agent {
	t.Helper()
	mem := memory.NewInMemStore()
	t.Cleanup(func() { mem.Close() })

	logger := slog.New(slog.NewTextHandler(testLogWriter{t}, nil))
	cfg := agents.Config{
		ID:        id,
		Role:      agents.Researcher,
		Goal:      "test",
		DependsOn: dependsOn,
		Timeout:   5 * time.Second,
		Restart:   agents.OneForOne,
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

func TestSupervisor_New_DefaultLimits(t *testing.T) {
	sup := New(nil, agents.OneForOne, 0, 0, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if sup.maxRestarts != 3 {
		t.Errorf("maxRestarts = %d, want 3", sup.maxRestarts)
	}
	if sup.restartWindow != time.Minute {
		t.Errorf("restartWindow = %v, want 1m", sup.restartWindow)
	}
}

func TestSupervisor_FindDependents_Transitive(t *testing.T) {
	// a ← b ← c ; d also depends on a
	a := newTestAgentWithDeps(t, "a", nil)
	b := newTestAgentWithDeps(t, "b", []string{"a"})
	c := newTestAgentWithDeps(t, "c", []string{"b"})
	d := newTestAgentWithDeps(t, "d", []string{"a"})
	list := []*agents.Agent{a, b, c, d}
	sup := &Supervisor{agents: list}

	deps := sup.findDependents("a")
	if len(deps) != 3 {
		t.Fatalf("findDependents(a) = %v (len %d), want 3 ids", deps, len(deps))
	}
	seen := map[string]bool{}
	for _, id := range deps {
		seen[id] = true
	}
	for _, want := range []string{"b", "c", "d"} {
		if !seen[want] {
			t.Errorf("missing dependent %q in %v", want, deps)
		}
	}
}

func TestSupervisor_OneForAll_RestartsAll(t *testing.T) {
	ag1 := newTestAgent(t, "agent-1")
	ag2 := newTestAgent(t, "agent-2")
	logger := slog.New(slog.NewTextHandler(testLogWriter{t}, nil))
	sup := New([]*agents.Agent{ag1, ag2}, agents.OneForAll, 3, time.Minute, logger)
	ctx, cancel := context.WithCancel(context.Background())
	sup.Start(ctx)
	t.Cleanup(func() {
		cancel()
		sup.Stop()
	})
	time.Sleep(30 * time.Millisecond)

	report, replyCh := failureReport("agent-1", fmt.Errorf("boom"))
	sup.FailureReports <- report
	decision := <-replyCh
	if !decision.Retry {
		t.Fatal("expected Retry for OneForAll")
	}

	time.Sleep(30 * time.Millisecond)
	for _, ag := range []*agents.Agent{ag1, ag2} {
		select {
		case ag.Inbox <- agents.Message{RunID: "r", Input: "ping"}:
		default:
			t.Errorf("agent %s inbox blocked after OneForAll restart", ag.ID())
		}
	}
}

func TestSupervisor_RestForOne_RestartsSubtree(t *testing.T) {
	// root → mid → leaf; failing mid should restart mid + leaf
	root := newTestAgentWithDeps(t, "root", nil)
	mid := newTestAgentWithDeps(t, "mid", []string{"root"})
	leaf := newTestAgentWithDeps(t, "leaf", []string{"mid"})
	logger := slog.New(slog.NewTextHandler(testLogWriter{t}, nil))
	sup := New([]*agents.Agent{root, mid, leaf}, agents.RestForOne, 3, time.Minute, logger)
	ctx, cancel := context.WithCancel(context.Background())
	sup.Start(ctx)
	t.Cleanup(func() {
		cancel()
		sup.Stop()
	})
	time.Sleep(30 * time.Millisecond)

	report, replyCh := failureReport("mid", fmt.Errorf("mid failed"))
	sup.FailureReports <- report
	decision := <-replyCh
	if !decision.Retry {
		t.Fatal("expected Retry for RestForOne")
	}
	time.Sleep(30 * time.Millisecond)
	for _, id := range []string{"mid", "leaf"} {
		var ag *agents.Agent
		switch id {
		case "mid":
			ag = mid
		case "leaf":
			ag = leaf
		}
		select {
		case ag.Inbox <- agents.Message{RunID: "r", Input: "ping"}:
		default:
			t.Errorf("agent %s inbox blocked after RestForOne restart", id)
		}
	}
	// root should still accept inbox (not restarted)
	select {
	case root.Inbox <- agents.Message{RunID: "r", Input: "ping"}:
	default:
		t.Error("root inbox blocked — should not be restarted for mid failure")
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
