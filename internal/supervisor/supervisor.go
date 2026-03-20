package supervisor

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Ad3bay0c/routex/agents"
)

// Supervisor watches a group of agents and applies a restart policy
// when any one of them fails.
//
// This is modelled after Erlang/OTP supervision trees — one of the most
// battle-tested patterns for building reliable concurrent systems.
// Erlang systems famously achieve "nine nines" uptime (99.9999999%)
// using exactly this idea: let things fail, detect the failure fast,
// restart cleanly.
//
// The Supervisor does not prevent failures — it survives them.
//
// internal/ means this package is private to routex.
// Code outside the routex module cannot import it directly.
// Users interact with supervision through the Runtime, not here.
type Supervisor struct {
	// agents is the list of agents this supervisor manages.
	// Ordered by definition — the scheduler handles run order separately.
	agents []*agents.Agent

	// policy is the default restart policy applied when any agent fails.
	// Individual agents can override this with their own policy in AgentConfig.
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

	// mu protects restartCounts from concurrent access.
	// Multiple agents can fail at the same time — this keeps the
	// restart count map safe.
	mu sync.Mutex

	logger *slog.Logger
}

// agentEntry pairs an agent with its running goroutine's cancel function.
// Stored so the supervisor can cancel a specific agent when applying
// rest_for_one or one_for_all restart policies.
type agentEntry struct {
	agent  *agents.Agent
	cancel context.CancelFunc
}

// New creates a Supervisor for the given agents.
// Called by the runtime after all agents have been constructed.
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
		agents:        agentList,
		policy:        policy,
		maxRestarts:   maxRestarts,
		restartWindow: restartWindow,
		restartCounts: make(map[string][]time.Time),
		logger:        logger.With("component", "supervisor"),
	}
}

// Start launches all agents as goroutines and monitors them.
// Returns a channel that receives an error if the supervisor gives up
// on an agent after exhausting all restarts — the runtime reads this
// channel to know when something has permanently failed.
//
// Start is non-blocking — it returns immediately after launching goroutines.
// The caller (runtime) waits on the returned error channel.
func (s *Supervisor) Start(ctx context.Context) <-chan error {
	errCh := make(chan error, 1)

	// Launch the monitoring goroutine
	go s.monitor(ctx, errCh)

	return errCh
}

// monitor starts all agents and watches for failures.
// This runs in its own goroutine — it is the supervisor's heartbeat.
//
// It listens on every agent's Output() channel simultaneously using
// a fan-in pattern. When an agent finishes with an error, it calls
// HandleFailure to apply the restart policy. If the agent has exhausted
// its restart budget, it sends the terminal error to errCh so the
// runtime knows the run has permanently failed.
func (s *Supervisor) monitor(ctx context.Context, errCh chan<- error) {
	// entries holds each agent paired with a way to cancel it individually
	entries := make(map[string]*agentEntry, len(s.agents))

	// agentResults fans all agent Output() channels into one channel
	// so monitor() can wait on all of them in a single select statement.
	// Buffer size = number of agents so no agent blocks on send.
	agentResults := make(chan agents.Result, len(s.agents))

	// Start every agent in its own goroutine and wire up result fan-in
	for _, a := range s.agents {
		agentCtx, cancel := context.WithCancel(ctx)
		entries[a.ID()] = &agentEntry{
			agent:  a,
			cancel: cancel,
		}

		// For each agent, launch a forwarder goroutine that reads from
		// the agent's own output channel and forwards into agentResults.
		// This is the fan-in pattern — many sources, one destination.
		go func(ag *agents.Agent) {
			for result := range ag.Notify {
				agentResults <- result
			}
		}(a)

		go a.Run(agentCtx)
		s.logger.Info("agent launched", "agent_id", a.ID())
	}

	// Watch for results and failures until context is cancelled
	for {
		select {
		case <-ctx.Done():
			// Runtime is shutting down — cancel all agents and exit
			s.logger.Info("supervisor stopping", "reason", ctx.Err())
			for id, entry := range entries {
				entry.cancel()
				s.logger.Debug("agent cancelled", "agent_id", id)
			}
			return

		case result := <-agentResults:
			// An agent finished — check if it failed
			if result.Err == nil {
				s.logger.Info("agent completed successfully", "agent_id", result.AgentID)
				continue
			}

			// Agent failed — apply restart policy
			agentMap := make(map[string]*agents.Agent, len(s.agents))
			for _, a := range s.agents {
				agentMap[a.ID()] = a
			}

			if err := s.HandleFailure(ctx, result.AgentID, result.Err, agentMap); err != nil {
				// Restart budget exhausted — this is a permanent failure.
				// Send to errCh so the runtime can abort the run.
				// Non-blocking send: if the runtime already received one
				// permanent failure, we do not block trying to send another.
				select {
				case errCh <- err:
				default:
				}
			}
		}
	}
}

// HandleFailure is called by the runtime when an agent's result
// carries a non-nil error. The supervisor decides what to do next
// based on the agent's restart policy.
//
// Returns an error if the agent has exhausted its restart budget —
// meaning the runtime should abort the entire run.
func (s *Supervisor) HandleFailure(
	ctx context.Context,
	failedID string,
	failErr error,
	entries map[string]*agents.Agent,
) error {
	s.logger.Warn("agent failed",
		"agent_id", failedID,
		"error", failErr,
	)

	// Check restart budget first — before applying any policy
	if err := s.checkRestartBudget(failedID); err != nil {
		return fmt.Errorf("agent %q permanently failed: %w", failedID, err)
	}

	// Record this restart attempt
	s.recordRestart(failedID)

	// Find the failed agent's config to get its specific restart policy
	var failedAgent *agents.Agent
	for _, a := range s.agents {
		if a.ID() == failedID {
			failedAgent = a
			break
		}
	}
	if failedAgent == nil {
		return fmt.Errorf("supervisor: unknown agent %q", failedID)
	}

	// Apply the restart policy
	switch s.policy {

	case agents.OneForOne:
		// Restart only the failed agent — others keep running
		s.logger.Info("applying one_for_one restart", "agent_id", failedID)
		agentCtx, _ := context.WithCancel(ctx) //nolint:govet
		go failedAgent.Run(agentCtx)

	case agents.OneForAll:
		// Restart every agent in the crew
		s.logger.Info("applying one_for_all restart", "agent_id", failedID)
		for _, a := range s.agents {
			agentCtx, _ := context.WithCancel(ctx) //nolint:govet
			go a.Run(agentCtx)
		}

	case agents.RestForOne:
		// Restart the failed agent and all agents that depend on it
		s.logger.Info("applying rest_for_one restart", "agent_id", failedID)
		toRestart := s.findDependents(failedID)
		toRestart = append([]string{failedID}, toRestart...)

		for _, id := range toRestart {
			if a, ok := entries[id]; ok {
				agentCtx, _ := context.WithCancel(ctx) //nolint:govet
				go a.Run(agentCtx)
				s.logger.Debug("restarting dependent", "agent_id", id)
			}
		}
	}

	return nil
}

// checkRestartBudget reports whether the agent has used up its
// allowed restarts within the restart window.
//
// We count only restarts that happened within the last restartWindow duration.
// Old restarts outside the window are not counted — an agent that failed
// 2 hours ago and just failed again gets a fresh budget.
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

	// Check if we have hit the limit
	if len(recent) >= s.maxRestarts {
		return fmt.Errorf(
			"restarted %d times in %s (limit: %d) — giving up",
			len(recent),
			s.restartWindow,
			s.maxRestarts,
		)
	}

	return nil
}

// recordRestart adds the current time to the agent's restart history.
func (s *Supervisor) recordRestart(agentID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.restartCounts[agentID] = append(s.restartCounts[agentID], time.Now())
}

// findDependents returns the IDs of all agents that depend on the given agent.
// Used by the rest_for_one policy to find which agents need restarting
// when their upstream dependency fails.
//
// Example crew: planner → writer → critic
// findDependents("planner") returns ["writer", "critic"]
// because writer depends on planner, and critic depends on writer.
func (s *Supervisor) findDependents(agentID string) []string {
	var dependents []string

	for _, a := range s.agents {
		for _, dep := range a.DependsOn() {
			if dep == agentID {
				// This agent depends on the failed one —
				// add it and recursively find its dependents too
				dependents = append(dependents, a.ID())
				dependents = append(dependents, s.findDependents(a.ID())...)
				break
			}
		}
	}

	return dependents
}
