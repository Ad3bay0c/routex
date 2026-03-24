package routex

import "time"

// Task is what you hand to the runtime when you want agents to do something.
type Task struct {
	// Input is the instruction — what do you want the agents to work on?
	// Example: "Write a report on the future of AI Agents in 2025"
	Input string

	// MaxDuration is the longest the entire crew is allowed to run.
	// If the job is not done by then, the runtime cancels it gracefully.
	// Zero means no limit.
	MaxDuration time.Duration

	// OutputFile is optional. If set, the final result is also written to this file.
	// Example: "report.md"
	OutputFile string

	// Metadata is a free-form map for anything extra you want to pass through
	// to agents without it being part of the main instruction.
	// Example: map[string]string{"language": "French", "tone": "formal"}
	Metadata map[string]string
}

// Result is what the runtime hands back to you when the crew finishes.
// It carries the final output plus everything you need to understand what happened.
type Result struct {
	// Output is the final text produced by the last agent in the chain.
	Output string

	// OutputFile is the path where the output was written, if configured.
	OutputFile string

	// Duration is how long the entire run took from start to finish.
	Duration time.Duration

	// TokensUsed is the total number of LLM tokens consumed across all agents.
	// Useful for tracking cost.
	TokensUsed int

	// TraceID is the OpenTelemetry trace ID for this run.
	TraceID string

	// AgentResults holds the individual output from each agent.
	// Key is the agent ID, value is what that agent produced.
	AgentResults map[string]AgentResult

	// Error holds the error if the run failed. nil means success.
	Error error
}

// AgentResult is the output from a single agent inside a run.
// You get one of these per agent in Result.AgentResults.
type AgentResult struct {
	// AgentID matches the id field you set in agents.yaml or AddAgent().
	AgentID string

	// Output is the text this specific agent produced.
	Output string

	// ToolCalls is a log of every tool this agent called during its run.
	ToolCalls []ToolCall

	// TokensUsed is the token count for just this agent.
	TokensUsed int

	// Duration is how long this specific agent ran.
	Duration time.Duration

	// Error is non-nil if this agent failed even after all retries.
	Error error
}

// ToolCall is a record of a single tool execution by an agent.
// Stored in AgentResult.ToolCalls so you can see exactly what the agent did.
type ToolCall struct {
	// ToolName matches the name you registered the tool with.
	ToolName string

	// Input is the raw JSON the LLM passed to the tool.
	Input string

	// Output is the raw JSON the tool returned.
	Output string

	// Duration is how long the tool call took.
	Duration time.Duration

	// Error is non-nil if the tool call failed.
	Error error
}
