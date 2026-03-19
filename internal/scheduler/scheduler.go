package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/Ad3bay0c/routex/agents"
)

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
	// ─────────────── validate the dependency graph ───────────────
	// Catch any reference to itself (a→a)
	// Catch cycles (a→b→a) before any agent runs
	if err := s.validateGraph(); err != nil {
		return nil, fmt.Errorf("scheduler: invalid dependency graph: %w", err)
	}

	// ─────────────── compute execution waves via topological sort ───────────────
	// A wave is a group of agents that can all run in parallel
	// because none of them depend on each other
	waveAgents, err := s.buildWaveAgents()
	if err != nil {
		return nil, fmt.Errorf("scheduler: %w", err)
	}

	s.logger.Info("execution plan ready", "waveAgents", len(waveAgents))
	for i, waves := range waveAgents {
		agentIDs := make([]string, len(waves))
		for j, agent := range waves {
			agentIDs[j] = agent.ID()
		}
		s.logger.Debug("wave", "number", i+1, "agents", agentIDs)
	}

	// ─────────────── execute each wave in order ───────────────
	// Agents within a wave run in parallel.
	// The next wave only starts when every agent in the current wave succeeds.
	results := make(map[string]agents.Result) // map[agentID]agents

	// The first agent in the first wave gets the raw user input.
	// Subsequent agents get the output of their dependencies.
	currentInput := initialInput

	for waveNum, waves := range waveAgents {
		s.logger.Info("starting wave", "number", waveNum+1, "size", len(waves))

		waveAgentResults, err := s.runWaveAgents(ctx, waves, currentInput, results)
		if err != nil {
			return results, fmt.Errorf("wave %d failed: %w", waveNum+1, err)
		}

		// Merge wave results into the overall results map
		for id, result := range waveAgentResults {
			results[id] = result
		}

		// The input for the next wave is the output of the last agent
		// in this wave — agents pass their work forward like a relay race
		if len(waves) > 0 {
			lastAgent := waves[len(waves)-1]
			if r, ok := waveAgentResults[lastAgent.ID()]; ok && r.Err == nil {
				currentInput = r.Output
			}
		}
	}

	return results, nil
}

// runWaveAgents executes a group of agents in parallel and waits for all of them.
// Returns a map of results for every agent in this wave.
// Returns an error if any agent in the wave fails — the caller decides
// whether to abort or continue based on restart policy.
func (s *Scheduler) runWaveAgents(
	ctx context.Context,
	waves []*agents.Agent,
	defaultInput string,
	previousResults map[string]agents.Result,
) (map[string]agents.Result, error) {
	// WaitGroup lets us wait for all agents in this wave to finish
	// before returning — like a starting gun and a finish line
	var wg sync.WaitGroup

	// Collect results from all agents in this wave
	// Using a mutex-protected map because multiple goroutines write to it
	var mu sync.Mutex
	waveResults := make(map[string]agents.Result, len(waves))

	for _, a := range waves {
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

// buildWaveAgents groups agents into ordered execution waves using
// a topological sort (Kahn's algorithm).
//
// Kahn's algorithm works like this:
//  1. Find all agents with no dependencies — they form wave 1
//  2. Remove those agents from the graph
//  3. Find all agents whose dependencies are now all satisfied — wave 2
//  4. Repeat until no agents remain
//
// If we run out of agents to place but some remain unplaced,
// there is a cycle in the dependency graph — we return an error.
func (s *Scheduler) buildWaveAgents() ([][]*agents.Agent, error) {
	// Build a map of agentID → number of unsatisfied dependencies
	// Agents with inDegree 0 are ready to run immediately
	inDegree := make(map[string]int, len(s.agents))
	// Build a map of agentID → agents that depend on it
	// When an agent finishes, we use this to decrement its dependents' inDegree
	dependents := make(map[string][]string, len(s.agents))
	// Quick lookup from ID to Agent pointer
	agentsByID := make(map[string]*agents.Agent, len(s.agents))

	for _, agent := range s.agents {
		agentsByID[agent.ID()] = agent
		inDegree[agent.ID()] = len(agent.DependsOn())
		for _, dep := range agent.DependsOn() {
			dependents[dep] = append(dependents[dep], agent.ID())
		}
	}

	var generalWaveAgents [][]*agents.Agent
	placed := 0

	for placed < len(s.agents) {
		// Find all agents that are ready this round (inDegree == 0)
		var waveAgents []*agents.Agent
		for _, a := range s.agents {
			if inDegree[a.ID()] == 0 {
				waveAgents = append(waveAgents, a)
			}
		}

		// No agents ready but some remain — cycle detected
		if len(waveAgents) == 0 {
			return nil, fmt.Errorf(
				"cycle detected in agent dependencies — " +
					"check your depends_on fields for circular references",
			)
		}

		generalWaveAgents = append(generalWaveAgents, waveAgents)
		placed += len(waveAgents)

		// Remove wave agents from the graph by setting their inDegree
		// to -1 (so they are not picked up in future rounds) and
		// decrementing the inDegree of everything that depended on them
		for _, a := range waveAgents {
			inDegree[a.ID()] = -1 // mark as placed
			for _, depID := range dependents[a.ID()] {
				inDegree[depID]--
			}
		}
	}

	return generalWaveAgents, nil
}

// validateGraph checks for two problems before any agent runs:
//  1. Missing references — depends_on names an agent that does not exist
//  2. Cycles — a→b→a would cause the scheduler to wait forever
//
// Cycle detection uses DFS with three-colour marking — the standard algorithm
// for finding cycles in a directed graph:
//
//	white (0) — not yet visited
//	grey  (1) — currently being explored (on the DFS stack right now)
//	black (2) — fully explored, no cycle through this node
//
// If we reach a grey node while exploring, we have found a cycle —
// we are currently inside a path that leads back to a node we are
// still processing. We record the path so the error message names
// the exact agents involved.
func (s *Scheduler) validateGraph() error {
	ids := make(map[string]bool, len(s.agents))
	for _, a := range s.agents {
		ids[a.ID()] = true
	}

	// First pass — check all depends_on references point to real agents
	for _, a := range s.agents {
		for _, dep := range a.DependsOn() {
			if !ids[dep] {
				return fmt.Errorf(
					"agent %q depends_on %q but no agent with that id exists",
					a.ID(), dep,
				)
			}
		}
	}

	// Build adjacency map for DFS: agentID → list of agents it depends on
	adj := make(map[string][]string, len(s.agents))
	for _, a := range s.agents {
		adj[a.ID()] = a.DependsOn()
	}

	// Three-colour DFS state
	const (
		white = 0 // unvisited
		grey  = 1 // on current DFS path — if we see this again, cycle found
		black = 2 // fully explored — safe
	)
	colour := make(map[string]int, len(s.agents))

	// path tracks the current DFS stack so we can name the cycle in the error
	var path []string

	// visit explores a node and all its dependencies recursively
	// Returns the cycle path if one is found, nil otherwise
	var visit func(id string) []string
	visit = func(id string) []string {
		colour[id] = grey
		path = append(path, id)

		for _, dep := range adj[id] {
			switch colour[dep] {
			case grey:
				// We reached a node that is currently on our DFS stack —
				// this is a cycle. Build the cycle description from the path.
				// Find where the cycle starts in the path
				cycleStart := -1
				for i, p := range path {
					if p == dep {
						cycleStart = i
						break
					}
				}
				if cycleStart >= 0 {
					return append(path[cycleStart:], dep) // e.g. [a, b, a]
				}
				return append(path, dep)

			case white:
				// Not yet visited — recurse
				if cycle := visit(dep); cycle != nil {
					return cycle
				}
			}
			// black — already fully explored, known safe, skip
		}

		// Done exploring this node — mark black and remove from path
		colour[id] = black
		path = path[:len(path)-1]
		return nil
	}

	// Run DFS from every unvisited node
	// We need to start from every node because the graph may not be connected —
	// there could be isolated groups of agents that never touch each other
	for _, a := range s.agents {
		if colour[a.ID()] == white {
			if cycle := visit(a.ID()); cycle != nil {
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
