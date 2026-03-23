package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/Ad3bay0c/routex/agents"
	"github.com/Ad3bay0c/routex/internal/supervisor"
)

// agentNode is the minimal interface the scheduling algorithms need.
// *agents.Agent satisfies this — test stubs do too.
type agentNode interface {
	ID() string
	DependsOn() []string
}

// Scheduler determines agent execution order and drives the wave-by-wave
// execution. It cooperates with the Supervisor on failures:
//
//	Success flow:
//	  scheduler sends task → agent.Inbox
//	  agent processes    → result → agent.Output()
//	  scheduler reads    ← agent.Output()
//	  if success         → continue to next wave
//
//	Failure flow:
//	  scheduler reads error result ← agent.Output()
//	  scheduler asks supervisor    → FailureReport{Reply: replyCh}
//	  supervisor restarts agent    → Decision{Retry:true}
//	  scheduler re-sends task      → agent.Inbox  (agent is fresh, waiting)
//	  scheduler waits again        ← agent.Output()
//	  ... repeats until success or Decision{Err: ...}
//
// This means the scheduler never advances a wave past a failed agent
// — it waits for the supervisor's decision first.
type Scheduler struct {
	agents     []*agents.Agent
	supervisor *supervisor.Supervisor
	logger     *slog.Logger
}

// New creates a Scheduler for the given agents.
func New(
	agentList []*agents.Agent,
	sup *supervisor.Supervisor,
	logger *slog.Logger,
) *Scheduler {
	return &Scheduler{
		agents:     agentList,
		supervisor: sup,
		logger:     logger.With("component", "scheduler"),
	}
}

// Run executes the full agent crew in dependency order.
// Blocks until all agents complete or context is cancelled.
//
// For each wave of agents that are ready to run (all their dependencies
// have finished), Run sends the appropriate input to each agent's Inbox
// and waits for their results before moving to the next wave.
//
// Returns a map of agentID → Result containing every agent's output.
// The runtime uses this to build the final Result returned to the caller.
func (s *Scheduler) Run(ctx context.Context, initialInput string) (map[string]agents.Result, error) {
	// ── validate the dependency graph ──
	// Catch cycles (a→b→a) before any agent runs.
	if err := s.validateGraph(); err != nil {
		return nil, fmt.Errorf("scheduler: invalid dependency graph: %w", err)
	}

	// ── compute execution waves ──
	// A wave is a group of agents that can all run in parallel
	// because none of them depend on each other.
	waves, err := s.buildWaves()
	if err != nil {
		return nil, fmt.Errorf("scheduler: %w", err)
	}

	s.logger.Info("execution plan ready", "waves", len(waves))

	results := make(map[string]agents.Result)
	currentInput := initialInput

	// ── execute each wave in order ──
	// Multiple agents within a wave run in parallel.
	for waveNum, wave := range waves {
		s.logger.Info("starting wave", "number", waveNum+1, "size", len(wave))

		waveResults, err := s.runWave(ctx, wave, currentInput, results)
		if err != nil {
			return results, fmt.Errorf("wave %d failed: %w", waveNum+1, err)
		}

		for id, result := range waveResults {
			results[id] = result
		}

		// Compute defaultInput for the next wave — the output of the last agent.
		if len(wave) > 0 {
			lastAgent := wave[len(wave)-1]
			if r, ok := waveResults[lastAgent.ID()]; ok && r.Err == nil {
				currentInput = r.Output
			}
		}
	}

	return results, nil
}

// runWave runs all agents in the wave in parallel.
// For each agent, it handles the full failure+retry loop before
// considering that agent slot "done". The wave does not advance
// until every agent slot is resolved (success or permanent failure).
func (s *Scheduler) runWave(
	ctx context.Context,
	wave []*agents.Agent,
	defaultInput string,
	previousResults map[string]agents.Result,
) (map[string]agents.Result, error) {
	var wg sync.WaitGroup
	var mu sync.Mutex

	waveResults := make(map[string]agents.Result, len(wave))
	// waveErr captures the first permanent failure in this wave.
	var waveErr error

	for _, a := range wave {
		wg.Add(1)

		// Launch each agent concurrently.
		go func(ag *agents.Agent) {
			defer wg.Done()

			result, err := s.runAgentUntilDone(ctx, ag, defaultInput, previousResults)

			mu.Lock()
			defer mu.Unlock()

			waveResults[ag.ID()] = result
			if err != nil && waveErr == nil {
				waveErr = err
			}
		}(a)
	}

	wg.Wait()

	if waveErr != nil {
		return waveResults, waveErr
	}
	return waveResults, nil
}

// runAgentUntilDone handles the full lifecycle of one agent in a wave:
// send task → wait for result → if error, ask supervisor → maybe retry.
// Returns only when the agent succeeds or permanently fails.
func (s *Scheduler) runAgentUntilDone(
	ctx context.Context,
	ag *agents.Agent,
	defaultInput string,
	previousResults map[string]agents.Result,
) (agents.Result, error) {
	input := s.resolveInput(ag, defaultInput, previousResults)

	for {
		// Check for context cancellation before each attempt.
		select {
		case <-ctx.Done():
			return agents.Result{AgentID: ag.ID(), Err: ctx.Err()}, ctx.Err()
		default:
		}

		s.logger.Info("dispatching agent",
			"agent_id", ag.ID(),
			"input_len", len(input),
		)

		// Send the task to the agent's Inbox.
		// Context is NOT sent in the message — it flows through the
		// agent's goroutine context set by the supervisor at Run() time.
		select {
		case ag.Inbox <- agents.Message{
			RunID: runIDFromContext(ctx),
			Input: input,
		}:
		case <-ctx.Done():
			return agents.Result{AgentID: ag.ID(), Err: ctx.Err()}, ctx.Err()
		}

		// Wait for the result on agent.Output() — only the scheduler reads this.
		var result agents.Result
		select {
		case result = <-ag.Output():
		case <-ctx.Done():
			return agents.Result{AgentID: ag.ID(), Err: ctx.Err()}, ctx.Err()
		}

		// Success — we are done with this agent.
		if result.Err == nil {
			s.logger.Info("agent completed", "agent_id", ag.ID())
			return result, nil
		}

		// Failure — ask the supervisor what to do.
		// We block here until the supervisor makes a decision.
		// The supervisor will either restart the agent (Retry:true)
		// or tell us to give up (Err != nil).
		s.logger.Warn("agent failed, consulting supervisor",
			"agent_id", ag.ID(),
			"error", result.Err,
		)

		replyCh := make(chan supervisor.Decision, 1)
		select {
		case s.supervisor.FailureReports <- supervisor.FailureReport{
			AgentID: ag.ID(),
			Err:     result.Err,
			Reply:   replyCh,
		}:
		case <-ctx.Done():
			return result, ctx.Err()
		}

		// Wait for the supervisor's decision.
		var decision supervisor.Decision
		select {
		case decision = <-replyCh:
		case <-ctx.Done():
			return result, ctx.Err()
		}

		if decision.Err != nil {
			// Supervisor gave up — propagate the permanent failure.
			s.logger.Error("agent permanently failed",
				"agent_id", ag.ID(),
				"error", decision.Err,
			)
			return result, decision.Err
		}

		if decision.Retry {
			// Supervisor restarted the agent — loop back and re-send the task.
			// input stays the same — the agent gets the same task again.
			s.logger.Info("supervisor restarted agent, retrying", "agent_id", ag.ID())
			continue
		}
	}
}

// resolveInput determines what input to send an agent.
//
// If the agent has no dependencies — it gets the raw user input.
// If it has one dependency — it gets that dependency's output.
// If it has multiple dependencies — we concatenate all their outputs
// so the agent has full context from everything it depends on.
func (s *Scheduler) resolveInput(
	a *agents.Agent,
	defaultInput string,
	previousResults map[string]agents.Result,
) string {
	deps := a.DependsOn()
	if len(deps) == 0 {
		return defaultInput
	}

	if len(deps) == 1 {
		if r, ok := previousResults[deps[0]]; ok {
			return r.Output
		}
		return defaultInput
	}

	// Multiple dependencies — label and combine all outputs.
	combined := ""
	for _, depID := range deps {
		if r, ok := previousResults[depID]; ok && r.Output != "" {
			combined += fmt.Sprintf("=== Output from %s ===\n%s\n\n", depID, r.Output)
		}
	}
	if combined == "" {
		return defaultInput
	}
	return combined
}

// buildWaves wraps the pure algorithm using the scheduler's agent list.
func (s *Scheduler) buildWaves() ([][]*agents.Agent, error) {
	nodes := make([]agentNode, len(s.agents))
	for i, a := range s.agents {
		nodes[i] = a
	}

	nodeWaves, err := buildWavesFromNodes(nodes)
	if err != nil {
		return nil, err
	}

	// Convert agentNodes [][]agentNode back to waves [][]*agents.Agent.
	waves := make([][]*agents.Agent, len(nodeWaves))
	for i, wave := range nodeWaves {
		waves[i] = make([]*agents.Agent, len(wave))
		for j, n := range wave {
			waves[i][j] = n.(*agents.Agent)
		}
	}
	return waves, nil
}

// validateGraph wraps the pure validation algorithm.
func (s *Scheduler) validateGraph() error {
	nodes := make([]agentNode, len(s.agents))
	for i, a := range s.agents {
		nodes[i] = a
	}
	return validateGraphNodes(nodes)
}

// ── Pure graph algorithms (testable without real agents) ──────────

// buildWavesFromNodes is the pure algorithm — works on agentNode interface.
// Testable without a real agents.Agent. Uses Kahn's topological sort.
func buildWavesFromNodes(nodes []agentNode) ([][]agentNode, error) {
	inDegree := make(map[string]int, len(nodes))
	dependents := make(map[string][]string, len(nodes))

	for _, n := range nodes {
		inDegree[n.ID()] = len(n.DependsOn())
		for _, dep := range n.DependsOn() {
			dependents[dep] = append(dependents[dep], n.ID())
		}
	}

	var waves [][]agentNode
	placed := 0

	for placed < len(nodes) {
		var wave []agentNode
		for _, n := range nodes {
			if inDegree[n.ID()] == 0 {
				wave = append(wave, n)
			}
		}
		if len(wave) == 0 {
			return nil, fmt.Errorf(
				"cycle detected in agent dependencies — " +
					"check your depends_on fields for circular references",
			)
		}

		waves = append(waves, wave)
		placed += len(wave)

		for _, n := range wave {
			inDegree[n.ID()] = -1
			for _, depID := range dependents[n.ID()] {
				inDegree[depID]--
			}
		}
	}

	return waves, nil
}

// validateGraphNodes is the pure validation algorithm — works on agentNode.
// Checks for missing references and cycles using three-colour DFS.
func validateGraphNodes(nodes []agentNode) error {
	ids := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		ids[n.ID()] = true
	}

	for _, n := range nodes {
		for _, dep := range n.DependsOn() {
			if !ids[dep] {
				return fmt.Errorf(
					"agent %q depends_on %q but no agent with that id exists",
					n.ID(), dep,
				)
			}
		}
	}

	adj := make(map[string][]string, len(nodes))
	for _, n := range nodes {
		adj[n.ID()] = n.DependsOn()
	}

	const (
		white, grey, black = 0, 1, 2
	)
	colour := make(map[string]int, len(nodes))
	var path []string

	var visit func(id string) []string
	visit = func(id string) []string {
		colour[id] = grey
		path = append(path, id)

		for _, dep := range adj[id] {
			switch colour[dep] {
			case grey:
				cycleStart := -1
				for i, p := range path {
					if p == dep {
						cycleStart = i
						break
					}
				}
				if cycleStart >= 0 {
					return append(path[cycleStart:], dep)
				}
				return append(path, dep)
			case white:
				if cycle := visit(dep); cycle != nil {
					return cycle
				}
			}
		}

		colour[id] = black
		path = path[:len(path)-1]
		return nil
	}

	for _, n := range nodes {
		if colour[n.ID()] == white {
			if cycle := visit(n.ID()); cycle != nil {
				return fmt.Errorf(
					"cycle detected in agent dependencies: %v\n"+
						"  agents cannot form circular depends_on chains",
					formatCycle(cycle),
				)
			}
		}
	}

	return nil
}

func formatCycle(cycle []string) string {
	result := ""
	for i, id := range cycle {
		if i > 0 {
			result += " → "
		}
		result += id
	}
	return result
}

type runIDKey struct{}

func WithRunID(ctx context.Context, runID string) context.Context {
	return context.WithValue(ctx, runIDKey{}, runID)
}

func runIDFromContext(ctx context.Context) string {
	if id, ok := ctx.Value(runIDKey{}).(string); ok {
		return id
	}
	return "unknown-run"
}
