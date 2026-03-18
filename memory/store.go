package memory

import (
	"context"
	"fmt"
	"time"
)

// MemoryStore is the contract every memory backend must satisfy.
//
// Routex agents have two kinds of memory — and this interface serves both:
//
//  1. Short-term memory — the conversation history within a single run.
//     The agent keeps adding messages as it thinks step by step.
//     When the run ends, short-term memory is discarded.
//
//  2. Long-term memory — facts and outputs stored across runs.
//     A writer agent saves its finished report. A planner saves a
//     summary. The next run can retrieve these and build on them.
//
// Two backends implement this interface:
//   - InMemStore  (memory/inmem.go)  — fast, no dependencies, lost on restart
//   - RedisStore  (memory/redis.go)  — persistent, survives restarts, shareable
type MemoryStore interface {
	// Set stores a value under a key.
	// ttl controls how long the value lives. Zero means no expiry.
	// Use namespaced keys to avoid collisions between agents:
	//   "planner:plan"   "writer:draft"   "critic:review"
	Set(ctx context.Context, key string, value string, ttl time.Duration) error

	// Get retrieves a value by key.
	// Returns ErrNotFound if the key does not exist or has expired.
	// Agents check for ErrNotFound to know whether to start fresh
	// or continue from a previous run.
	Get(ctx context.Context, key string) (string, error)

	// Delete removes a key immediately.
	// Agents call this to clean up sensitive intermediate data
	// after a run completes — e.g. scraped content, API responses.
	Delete(ctx context.Context, key string) error

	// Append adds a message to a named conversation history list.
	// Each agent maintains its own history list keyed by agent ID.
	// The list grows as the agent thinks — each LLM turn adds an entry.
	// Example key: "agent:writer:history"
	Append(ctx context.Context, key string, message Message) error

	// History retrieves the full conversation history for a key.
	// The agent passes this to the LLM on every call so it remembers
	// what it has already thought and done in this run.
	// limit controls how many recent messages to return. Zero means all.
	History(ctx context.Context, key string, limit int) ([]Message, error)

	// ClearHistory removes all messages from a history list.
	// Called by the runtime when an agent restarts after a crash —
	// we want a clean slate, not a confused continuation.
	ClearHistory(ctx context.Context, key string) error

	// Close releases any resources held by the backend.
	// Called by the runtime during graceful shutdown.
	Close() error
}

// Message is a single entry in an agent's conversation history.
// It mirrors the message format that LLM APIs use — role + content.
type Message struct {
	// Role is who sent this message.
	// "user" means input coming in to the agent (task, tool results).
	// "assistant" means output the LLM produced.
	// "system" means instructions from the runtime (rarely added here).
	Role string

	// Content is the text of the message.
	Content string

	// Timestamp records when this message was added.
	// Useful for debugging — you can see exactly when each thought happened.
	Timestamp time.Time

	// ToolCall is non-nil when this message records a tool being called.
	// Stored so the agent can show the LLM the full tool call history.
	ToolCall *ToolCallRecord
}

// ToolCallRecord is embedded in a Message when that message records a tool use.
// LLMs need to see both the tool call and the tool result in the history
// to reason correctly about what has already been done.
type ToolCallRecord struct {
	// ToolName is the name of the tool that was called.
	ToolName string

	// Input is the JSON that was sent to the tool.
	Input string

	// Output is the JSON the tool returned.
	Output string

	// Error is non-empty if the tool call failed.
	Error string
}

// ErrNotFound is returned by Get when the key does not exist.
// Agents check for this specifically so they know the difference between
// "key not found" (start fresh) and "something broke" (return an error).
//
// Usage:
//
//	val, err := store.Get(ctx, "planner:plan")
//	if errors.Is(err, memory.ErrNotFound) {
//	    // no previous plan — start from scratch
//	}
var ErrNotFound = fmt.Errorf("key not found")

// AgentKey is a helper that builds a namespaced memory key for an agent.
// Prevents key collisions when multiple agents use the same store.
//
// Example:
//
//	AgentKey("writer", "draft")    →  "agent:writer:draft"
//	AgentKey("planner", "history") →  "agent:planner:history"
func AgentKey(agentID, suffix string) string {
	return fmt.Sprintf("agent:%s:%s", agentID, suffix)
}

// RunKey builds a key scoped to a specific run.
// Useful for storing data that should only live for the duration of one task.
//
// Example:
//
//	RunKey("abc123", "writer", "draft")  →  "run:abc123:writer:draft"
func RunKey(runID, agentID, suffix string) string {
	return fmt.Sprintf("run:%s:%s:%s", runID, agentID, suffix)
}
