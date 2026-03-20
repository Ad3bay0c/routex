package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/Ad3bay0c/routex/agents"
)

// agentNode is the minimal interface the scheduling algorithms need.
// *agents.Agent satisfies this — and so do test stubs.
// Keeping the algorithm on this interface makes it testable without
// importing the full agents package.
type agentNode interface {
	ID() string
	DependsOn() []string
}

// Scheduler determines the order agents run in based on their
// depends_on relationships and dispatches work to each agent
// at the right moment.
//
// The core algorithm is a topological sort — a way of ordering
// items that have dependencies. Given:
//
//	planner  (no dependencies)
//	writer   (depends on planner)
//	critic   (depends on writer)
//
// Topological sort produces: [planner, writer, critic]
// meaning planner runs first, then writer, then critic.
//
// If two agents have no dependency between them they can run
// in parallel — the scheduler launches them simultaneously.
//
// Example parallel crew:
//
//	researcher_a  (no dependencies)
//	researcher_b  (no dependencies)
//	writer        (depends on researcher_a AND researcher_b)
//
// researcher_a and researcher_b run at the same time.
// writer starts only after BOTH finish.
type Scheduler struct {
	agents []*agents.Agent
	logger *slog.Logger
}

// New creates a Scheduler for the given list of agents.
// Called by the runtime after the supervisor has been set up.
func New(agentList []*agents.Agent, logger *slog.Logger) *Scheduler {
	return &Scheduler{
		agents: agentList,
		logger: logger.With("component", "scheduler"),
	}
}

// Run executes the full agent crew in dependency order.
// It blocks until all agents have completed or the context is cancelled.
//
// For each wave of agents that are ready to run (all their dependencies
// have finished), Run sends the appropriate input to each agent's Inbox
// and waits for their results before moving to the next wave.
//
// Returns a map of agentID → Result containing every agent's output.
// The runtime uses this to build the final Result returned to the caller.
func (s *Scheduler) Run(ctx context.Context, initialInput string) (map[string]agents.Result, error) {
	// Step 1 — validate the dependency graph
	// Catch cycles (a→b→a) before any agent runs
	if err := s.validateGraph(); err != nil {
		return nil, fmt.Errorf("scheduler: invalid dependency graph: %w", err)
	}

	// Step 2 — compute execution waves via topological sort
	// A wave is a group of agents that can all run in parallel
	// because none of them depend on each other
	waves, err := s.buildWaves()
	if err != nil {
		return nil, fmt.Errorf("scheduler: %w", err)
	}

	s.logger.Info("execution plan ready", "waves", len(waves))
	for i, wave := range waves {
		ids := make([]string, len(wave))
		for j, a := range wave {
			ids[j] = a.ID()
		}
		s.logger.Debug("wave", "number", i+1, "agents", ids)
	}

	// Step 3 — execute each wave in order
	// Agents within a wave run in parallel.
	// The next wave only starts when every agent in the current wave succeeds.
	results := make(map[string]agents.Result)

	// The first agent in the first wave gets the raw user input.
	// Subsequent agents get the output of their dependencies.
	currentInput := initialInput

	for waveNum, wave := range waves {
		s.logger.Info("starting wave", "number", waveNum+1, "size", len(wave))

		waveResults, err := s.runWave(ctx, wave, currentInput, results)
		if err != nil {
			return results, fmt.Errorf("wave %d failed: %w", waveNum+1, err)
		}

		// Merge wave results into the overall results map
		for id, result := range waveResults {
			results[id] = result
		}

		// The input for the next wave is the output of the last agent
		// in this wave — agents pass their work forward like a relay race
		if len(wave) > 0 {
			lastAgent := wave[len(wave)-1]
			if r, ok := waveResults[lastAgent.ID()]; ok && r.Err == nil {
				currentInput = r.Output
			}
		}
	}

	return results, nil
}

// buildWaves groups agents into ordered execution waves.
// Delegates to buildWavesFromNodes using the scheduler's agent list.
func (s *Scheduler) buildWaves() ([][]*agents.Agent, error) {
	nodes := make([]agentNode, len(s.agents))
	for i, a := range s.agents {
		nodes[i] = a
	}

	nodeWaves, err := buildWavesFromNodes(nodes)
	if err != nil {
		return nil, err
	}

	// Convert [][]agentNode back to [][]*agents.Agent
	waves := make([][]*agents.Agent, len(nodeWaves))
	for i, wave := range nodeWaves {
		waves[i] = make([]*agents.Agent, len(wave))
		for j, n := range wave {
			waves[i][j] = n.(*agents.Agent)
		}
	}
	return waves, nil
}

// buildWavesFromNodes is the pure algorithm — works on agentNode interface.
// Testable without a real agents.Agent. Uses Kahn's topological sort.
func buildWavesFromNodes(nodes []agentNode) ([][]agentNode, error) {
	inDegree := make(map[string]int, len(nodes))
	dependents := make(map[string][]string, len(nodes))
	byID := make(map[string]agentNode, len(nodes))

	for _, n := range nodes {
		byID[n.ID()] = n
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

// validateGraph checks the scheduler's agent list for problems.
func (s *Scheduler) validateGraph() error {
	nodes := make([]agentNode, len(s.agents))
	for i, a := range s.agents {
		nodes[i] = a
	}
	return validateGraphNodes(nodes)
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

// runWave executes a group of agents in parallel and waits for all of them.
// Returns a map of results for every agent in this wave.
// Returns an error if any agent in the wave fails — the caller decides
// whether to abort or continue based on restart policy.
func (s *Scheduler) runWave(
	ctx context.Context,
	wave []*agents.Agent,
	defaultInput string,
	previousResults map[string]agents.Result,
) (map[string]agents.Result, error) {
	// WaitGroup lets us wait for all agents in this wave to finish
	// before returning — like a starting gun and a finish line
	var wg sync.WaitGroup

	// Collect results from all agents in this wave
	// Using a mutex-protected map because multiple goroutines write to it
	var mu sync.Mutex
	waveResults := make(map[string]agents.Result, len(wave))

	for _, a := range wave {
		wg.Add(1)

		// Launch each agent concurrently
		// The closure captures `a` by value via the loop variable
		// We pass `a` as a parameter to avoid the classic Go loop variable capture bug
		go func(ag *agents.Agent) {
			defer wg.Done()

			// Determine what input to send this agent.
			// If it has dependencies, use the output from its first dependency.
			// Otherwise use the default input passed to this wave.
			input := s.resolveInput(ag, defaultInput, previousResults)

			s.logger.Info("dispatching agent",
				"agent_id", ag.ID(),
				"input_len", len(input),
			)

			// Send work to the agent's inbox
			ag.Inbox <- agents.Message{
				RunID: runIDFromContext(ctx),
				Input: input,
				// Note: we embed ctx in the Message so the agent's
				// think() loop can check for cancellation
			}

			// Wait for the agent to finish and collect its result
			// This read blocks until the agent sends to its output channel
			result := <-ag.Output()

			mu.Lock()
			waveResults[ag.ID()] = result
			mu.Unlock()

			if result.Err != nil {
				s.logger.Warn("agent finished with error",
					"agent_id", ag.ID(),
					"error", result.Err,
				)
			} else {
				s.logger.Info("agent finished successfully",
					"agent_id", ag.ID(),
				)
			}
			// sends this to notify supervisor about the result
			ag.Notify <- result
		}(a)
	}

	// Block until every agent in this wave has finished
	wg.Wait()

	// Check if any agent in this wave failed
	for id, result := range waveResults {
		if result.Err != nil {
			return waveResults, fmt.Errorf("agent %q failed: %w", id, result.Err)
		}
	}

	return waveResults, nil
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

	// Multiple dependencies — combine all outputs
	// Label each section clearly so the agent knows which input
	// came from which upstream agent
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

// runIDFromContext extracts the run ID from the context if present.
// Falls back to a placeholder if not set — the runtime always sets it.
func runIDFromContext(ctx context.Context) string {
	if id, ok := ctx.Value(runIDKey{}).(string); ok {
		return id
	}
	return "unknown-run"
}

// runIDKey is the context key type for the run ID.
// Using a private struct type prevents key collisions with other packages
// that might also store values in the same context.
type runIDKey struct{}

// WithRunID returns a new context carrying the given run ID.
// Called by the runtime before passing ctx to the scheduler.
func WithRunID(ctx context.Context, runID string) context.Context {
	return context.WithValue(ctx, runIDKey{}, runID)
}

// formatCycle turns a cycle path like ["a", "b", "a"] into
// a readable string like "a → b → a" for the error message.
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
