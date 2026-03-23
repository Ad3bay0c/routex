package agents

import (
	"fmt"
	"time"

	"github.com/Ad3bay0c/routex/llm"
)

// Config holds the settings for a single agent.
// Built by config.go when parsing agents.yaml,
// or filled in directly when using the programmatic API.
type Config struct {
	// ID is the unique name for this agent within a crew.
	// Used as a key in logs, traces, and Result.AgentResults.
	// Example: "planner", "writer", "critic"
	ID string

	// Role determines the agent's system prompt and behaviour.
	// Must be one of the Role constants defined in roles.go.
	Role Role

	// Goal is appended to the system prompt to give the agent
	// a specific objective within its role.
	// Example: "Research and write a detailed report on the given topic"
	Goal string

	// Tools is the list of tool names this agent is allowed to use.
	// Each name must match a tool registered with rt.RegisterTool().
	// Example: []string{"web_search", "write_file"}
	Tools []string

	// DependsOn is a list of agent IDs that must complete successfully
	// before this agent is allowed to start.
	// The scheduler reads this to build the execution order.
	// Example: []string{"planner"}  — writer waits for planner
	DependsOn []string

	// MaxRetries is how many times the runtime will restart this agent
	// if it fails, before giving up and returning an error.
	MaxRetries int

	// Timeout is how long this agent is allowed to run in total.
	// Includes all LLM calls and tool executions.
	// Zero means no timeout.
	Timeout time.Duration

	// Restart is the policy that controls what happens when this agent fails.
	Restart RestartPolicy

	// MaxDuplicateToolCalls is how many times the same tool+input combination
	// is allowed per thinking attempt before the agent is redirected.
	// Defaults to 2 if zero — allows one retry but blocks infinite loops.
	MaxDuplicateToolCalls int

	// MaxTotalToolCalls is the absolute tool call budget per thinking attempt.
	// Guards against runaway agents that vary inputs but still loop excessively.
	// Defaults to 20 if zero.
	MaxTotalToolCalls int

	// LLM is an optional per-agent LLM configuration.
	// When set, this agent uses its own LLM provider/model instead of
	// the runtime default. This allows different agents in the same crew
	// to use different models — e.g. a fast cheap model for research
	// and a powerful model for final synthesis.
	//
	// Example agents.yaml:
	//   llm:
	//     provider: "openai"
	//     model:    "gpt-4o"
	//     api_key:  "env:OPENAI_API_KEY"
	//
	// Nil means use the runtime's default LLM adapter.
	LLM *llm.Config
}

// RestartPolicy defines what the supervisor does when an agent crashes.
// Modelled after Erlang/OTP supervision strategies.
type RestartPolicy string

const (
	// OneForOne restarts only the agent that crashed.
	// The other agents in the crew continue running unaffected.
	// Use this when agents are independent of each other.
	OneForOne RestartPolicy = "one_for_one"

	// OneForAll restarts every agent in the crew when any one crashes.
	// Use this when agents are tightly coupled and a partial crew
	// produces incorrect results.
	OneForAll RestartPolicy = "one_for_all"

	// RestForOne restarts the crashed agent plus all agents that
	// depend on it (directly or transitively).
	// Use this for pipeline-style crews where a downstream agent
	// cannot continue without its upstream dependencies.
	RestForOne RestartPolicy = "rest_for_one"
)

// ParseRestartPolicy converts a string from agents.yaml into a RestartPolicy.
// Returns a clear error if the value is not recognised.
func ParseRestartPolicy(s string) (RestartPolicy, error) {
	// default to one_for_one if not set — safest choice
	if s == "" {
		return OneForOne, nil
	}
	switch RestartPolicy(s) {
	case OneForOne, OneForAll, RestForOne:
		return RestartPolicy(s), nil
	default:
		return "", fmt.Errorf(
			"unknown restart policy %q — valid values: one_for_one, one_for_all, rest_for_one",
			s,
		)
	}
}

// String returns the string representation of the restart policy.
func (r RestartPolicy) String() string {
	return string(r)
}
