package supervisor

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Ad3bay0c/routex/agents"
)

// Decision is what the supervisor sends back to the scheduler
// after handling a failure. The scheduler blocks waiting for this
// before deciding whether to continue, retry, or abort the wave.
type Decision struct {
	AgentID string
	// Retry is true when the supervisor has restarted the agent
	// and the scheduler should re-send the task to its Inbox.
	Retry bool
	// Err is non-nil when the agent has exhausted its restart budget.
	// The scheduler should abort the current run.
	Err error
}

// FailureReport is what the scheduler sends to the supervisor
// when an agent produces an error result.
type FailureReport struct {
	AgentID string
	Err     error
	// Reply is where the supervisor sends its Decision back.
	// Using a per-report channel ensures replies are matched to the
	// right failure even if multiple agents fail simultaneously.
	Reply chan<- Decision
}

// Supervisor watches agents via their Notify channels and makes
// restart decisions when failures are reported by the scheduler.
//
// The communication model:
//
//  1. Scheduler runs agent, reads result from agent.Output()
//  2. If result has error → scheduler sends FailureReport to supervisor
//  3. Supervisor checks restart budget
//     budget ok  → restarts agent goroutine, sends Decision{Retry:true}
//     budget gone → sends Decision{Err: ...}
//  4. Scheduler receives Decision:
//     Retry:true  → re-sends task to agent.Inbox, waits again
//     Err != nil  → aborts wave, propagates error to caller
//
// This way the scheduler never advances a wave until the supervisor
// has explicitly said the failure is resolved.
type Supervisor struct {
	// agents is the list of agents this supervisor manages.
	agents []*agents.Agent

	// policy is the default restart policy applied when any agent fails.
	policy agents.RestartPolicy

	// maxRestarts is how many times the supervisor will restart a failed
	// agent before giving up and returning an error to the runtime.
	maxRestarts int

	// restartWindow is the time window for counting restarts.
	// If an agent restarts more than maxRestarts times within this window,
	// the supervisor declares it permanently failed.
	// Example: 3 restarts within 1 minute = give up.
	restartWindow time.Duration

	// restartCounts tracks how many times each agent has been restarted.
	// Keyed by agent ID.
	restartCounts map[string][]time.Time

	// FailureReports is how the scheduler reports failed agents.
	// Buffered so the scheduler never blocks sending a report.
	FailureReports chan FailureReport

	mu     sync.Mutex
	wg     sync.WaitGroup // tracks all goroutines started by this supervisor
	logger *slog.Logger
}

// New creates a Supervisor for the given agents.
func New(
	agentList []*agents.Agent,
	policy agents.RestartPolicy,
	maxRestarts int,
	restartWindow time.Duration,
	logger *slog.Logger,
) *Supervisor {
	if maxRestarts == 0 {
		maxRestarts = 3
	}
	if restartWindow == 0 {
		restartWindow = time.Minute
	}
	return &Supervisor{
		agents:         agentList,
		policy:         policy,
		maxRestarts:    maxRestarts,
		restartWindow:  restartWindow,
		restartCounts:  make(map[string][]time.Time),
		logger:         logger.With("component", "supervisor"),
		FailureReports: make(chan FailureReport, len(agentList)),
	}
}

// Start launches all agent goroutines and begins processing
// FailureReports from the scheduler.
//
// No error channel is returned — permanent failures propagate through
// the scheduler's Decision channel back to the caller of Run().
// The supervisor's monitor goroutine is intentionally simple: if it
// panics the process should crash loudly rather than silently recover.
func (s *Supervisor) Start(ctx context.Context) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.monitor(ctx)
	}()
}

// Stop waits for the supervisor's monitor goroutine and all agent
// goroutines it spawned to finish. Call after cancelling the context
// passed to Start to avoid goroutine leaks and test races.
func (s *Supervisor) Stop() {
	s.wg.Wait()
}

// monitor starts all agents and watches two sources:
//  1. FailureReports from the scheduler — handles with a restart decision
//  2. ctx.Done() — shuts everything down cleanly
func (s *Supervisor) monitor(ctx context.Context) {
	agentsByID := make(map[string]*agents.Agent, len(s.agents))
	cancelFns := make(map[string]context.CancelFunc, len(s.agents))

	for _, agent := range s.agents {
		agentsByID[agent.ID()] = agent
		agentCtx, cancel := context.WithCancel(ctx)
		cancelFns[agent.ID()] = cancel
		s.wg.Add(1)
		go func(a *agents.Agent, c context.Context) {
			defer s.wg.Done()
			a.Run(c)
		}(agent, agentCtx)
		s.logger.Info("agent launched", "agent_id", agent.ID())
	}

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("supervisor stopping")
			for id, cancel := range cancelFns {
				cancel()
				s.logger.Debug("agent cancelled", "agent_id", id)
			}
			return

		case report := <-s.FailureReports:
			// Scheduler is blocked waiting for our decision — handle promptly
			decision := s.handleFailure(ctx, report.AgentID, report.Err, agentsByID, cancelFns)
			report.Reply <- decision
		}
	}
}

// handleFailure decides what to do when an agent fails.
// Called synchronously from monitor — the scheduler is waiting for the reply.
func (s *Supervisor) handleFailure(
	ctx context.Context,
	failedID string,
	failErr error,
	agentsByID map[string]*agents.Agent,
	cancelFns map[string]context.CancelFunc,
) Decision {
	s.logger.Warn("agent failed", "agent_id", failedID, "error", failErr)

	// Check restart budget before doing anything
	if err := s.checkRestartBudget(failedID); err != nil {
		return Decision{
			AgentID: failedID,
			Retry:   false,
			Err:     fmt.Errorf("agent %q permanently failed: %w", failedID, err),
		}
	}

	// Record this restart attempt
	s.recordRestart(failedID)

	switch s.policy {
	case agents.OneForOne:
		// Restart only the failed agent
		s.restartAgent(ctx, failedID, agentsByID, cancelFns)

	case agents.OneForAll:
		// Restart every agent in the crew
		for id := range agentsByID {
			s.restartAgent(ctx, id, agentsByID, cancelFns)
		}

	case agents.RestForOne:
		// Restart the failed agent and everything that depends on it
		toRestart := append([]string{failedID}, s.findDependents(failedID)...)
		for _, id := range toRestart {
			s.restartAgent(ctx, id, agentsByID, cancelFns)
		}
	}

	// Tell the scheduler: agent is restarted, re-send the task
	return Decision{
		AgentID: failedID,
		Retry:   true,
	}
}

// restartAgent cancels the old goroutine and starts a fresh one.
// The agent's Inbox and output channels are the same — the scheduler
// can re-send to Inbox immediately after receiving Decision{Retry:true}.
func (s *Supervisor) restartAgent(
	ctx context.Context,
	agentID string,
	agentsByID map[string]*agents.Agent,
	cancelFns map[string]context.CancelFunc,
) {
	if cancel, ok := cancelFns[agentID]; ok {
		cancel()
	}

	failedAgent, ok := agentsByID[agentID]
	if !ok {
		s.logger.Error("cannot restart unknown agent", "agent_id", agentID)
		return
	}

	// Start a fresh goroutine with a new context
	agentCtx, newCancel := context.WithCancel(ctx)
	cancelFns[agentID] = newCancel
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		failedAgent.Run(agentCtx)
	}()

	s.logger.Info("agent restarted", "agent_id", agentID, "policy", s.policy)
}

// checkRestartBudget reports whether the agent has used up its restart allowance
// within the sliding restart window.
func (s *Supervisor) checkRestartBudget(agentID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-s.restartWindow)

	// Filter out restart timestamps that are outside the window
	recent := s.restartCounts[agentID][:0]
	for _, t := range s.restartCounts[agentID] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}
	s.restartCounts[agentID] = recent

	if len(recent) >= s.maxRestarts {
		return fmt.Errorf(
			"restarted %d times within %s (limit: %d)",
			len(recent), s.restartWindow, s.maxRestarts,
		)
	}
	return nil
}

func (s *Supervisor) recordRestart(agentID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.restartCounts[agentID] = append(s.restartCounts[agentID], time.Now())
}

// findDependents returns all agent IDs that depend on the given agent,
// transitively. Used by the RestForOne policy.
func (s *Supervisor) findDependents(agentID string) []string {
	var dependents []string

	for _, a := range s.agents {
		for _, dep := range a.DependsOn() {
			if dep == agentID {
				dependents = append(dependents, a.ID())
				dependents = append(dependents, s.findDependents(a.ID())...)
				break
			}
		}
	}

	return dependents
}
