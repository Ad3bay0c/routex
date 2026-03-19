package llm

import (
	"context"
	"fmt"
	"time"

	"github.com/Ad3bay0c/routex/memory"
	"github.com/Ad3bay0c/routex/tools"
)

// Adapter is the contract every LLM provider must satisfy.
//
// Routex supports multiple LLM providers — Anthropic, OpenAI, Ollama.
// Each one has a different API, different authentication, different
// request and response shapes. This interface hides all of that.
//
// Agents never talk to a provider directly. They call Adapter.Complete()
// and get a Response back. Swapping providers — from Anthropic to Ollama,
// for example — means changing one line in agents.yaml. Zero agent code changes.
//
// Three structs implement this interface:
//   - AnthropicAdapter  (llm/anthropic.go)
//   - OpenAIAdapter     (llm/openai.go)
//   - OllamaAdapter     (llm/ollama.go)
type Adapter interface {
	// Complete sends a conversation history to the LLM and returns its response.
	// This is the core method — everything else in an agent's loop calls this.
	//
	// history is the full conversation so far — every message the agent has
	// sent and received, in order. The LLM needs all of it to reason correctly.
	//
	// schemas is the list of tools available to the agent this turn.
	// The LLM reads these and decides whether to respond with text
	// or with a tool call request.
	//
	// The returned Response tells the agent what to do next:
	//   - if Response.ToolCall is set  → call that tool, add result to history, call Complete again
	//   - if Response.Content is set   → the agent is done, return the content
	Complete(ctx context.Context, req Request) (Response, error)

	// Model returns the model string this adapter is configured to use.
	// Example: "claude-sonnet-4-6", "gpt-4o", "llama3"
	// Used in logs and traces so you always know which model ran.
	Model() string

	// Provider returns the provider name.
	// Example: "anthropic", "openai", "ollama"
	Provider() string
}

// Request is everything an LLM needs to produce a response.
// Built by the agent on every turn and passed to Adapter.Complete().
type Request struct {
	// SystemPrompt is the agent's identity — who it is and what its job is.
	// Set once from Role.SystemPrompt() when the agent starts.
	// Sent on every LLM call so the model never forgets its role.
	SystemPrompt string

	// History is the full conversation so far.
	// Grows by two messages per turn: one "user" message (input/tool result)
	// and one "assistant" message (the LLM's response).
	History []memory.Message

	// ToolSchemas describes the tools available this turn.
	// The LLM reads these to know what it can call.
	// Empty slice means no tools — the LLM can only respond with text.
	ToolSchemas map[string]tools.Schema

	// MaxTokens caps how long the LLM response can be.
	// Prevents runaway responses that burn tokens.
	// Zero lets the provider use its default.
	MaxTokens int

	// Temperature controls how creative vs deterministic the LLM is.
	// it should be between 0.0 and 1.0
	// 0.0 = very focused and deterministic (good for planners, executors)
	// 1.0 = more creative and varied (good for writers)
	// Most agents work well at 0.7.
	Temperature float64
}

// Response is what the LLM sends back after a Complete() call.
// The agent inspects this to decide its next action.
type Response struct {
	// Content is the LLM's text response.
	// Non-empty when the LLM is done thinking and has a final answer.
	// Empty when the LLM wants to call a tool instead.
	Content string

	// ToolCall is non-nil when the LLM wants to call a tool.
	// The agent should execute the tool, add the result to history,
	// then call Complete() again. This loop continues until
	// Content is non-empty — meaning the LLM is done.
	ToolCall *ToolCallRequest

	// Usage records how many tokens were consumed in this call.
	// Accumulated across all turns to populate Result.TokensUsed.
	Usage TokenUsage

	// FinishReason tells you why the LLM stopped generating.
	// Common values: "end_turn", "tool_use", "max_tokens", "stop"
	// "max_tokens" means the response was cut off — consider raising MaxTokens.
	FinishReason string
}

// ToolCallRequest is populated when the LLM decides to call a tool.
// The agent passes this to the tool registry to execute the call.
type ToolCallRequest struct {
	// ID is the unique identifier the LLM assigned to this tool call.
	// Must be sent back with the tool result so the LLM can match them up.
	// Anthropic and OpenAI both require this — do not discard it.
	ID string

	// ToolName matches one of the names in Request.ToolSchemas.
	// The agent uses this to look up the tool in the registry.
	ToolName string

	// Input is the raw JSON the LLM produced for the tool's parameters.
	// Passed directly to Tool.Execute() without modification.
	Input string
}

// TokenUsage records how many tokens a single LLM call consumed.
// Summed across all calls in a run to produce Result.TokensUsed.
type TokenUsage struct {
	// InputTokens is the number of tokens in the request
	// (system prompt + history + tool schemas).
	InputTokens int

	// OutputTokens is the number of tokens the LLM generated.
	OutputTokens int
}

// Total returns the sum of input and output tokens.
// This is the number that appears on your API bill.
func (u TokenUsage) Total() int {
	return u.InputTokens + u.OutputTokens
}

// Config holds the settings needed to create any LLM adapter.
// Passed to New() to build the right adapter for the configured provider.
type Config struct {
	// Provider selects which LLM backend to use.
	// Valid values: "anthropic", "openai", "ollama"
	Provider string

	// Model is the model identifier for the chosen provider.
	// Examples: "claude-sonnet-4-6", "gpt-4o", "llama3"
	Model string

	// APIKey is the authentication key for the provider.
	// For Ollama (local), this can be empty.
	// Read from environment — never hardcode this.
	APIKey string

	// BaseURL overrides the default API endpoint.
	// Useful for Ollama (http://localhost:11434) or
	// OpenAI-compatible proxies.
	// Empty means use the provider's default.
	BaseURL string

	// MaxTokens is the default token cap for every request.
	// Can be overridden per-request in Request.MaxTokens.
	MaxTokens int

	// Temperature is the default temperature for every request.
	// Can be overridden per-request in Request.Temperature.
	Temperature float64

	// Timeout is how long to wait for a single LLM call before giving up.
	// Zero means no timeout — not recommended in production.
	Timeout time.Duration
}

// New creates the right LLM adapter for the given config.
// This is the only place in the codebase that knows which adapter to build.
// All other code just holds an Adapter interface — no provider-specific types.
//
// Usage:
//
//	adapter, err := llm.New(llm.Config{
//	    Provider: "anthropic",
//	    Model:    "claude-sonnet-4-6",
//	    APIKey:   os.Getenv("ANTHROPIC_API_KEY"),
//	})
func New(cfg Config) (Adapter, error) {
	switch cfg.Provider {
	case "anthropic":
		return NewAnthropicAdapter(cfg)
	case "openai":
		return NewOpenAIAdapter(cfg)
	case "ollama":
		return NewOllamaAdapter(cfg)
	default:
		return nil, fmt.Errorf(
			"unknown llm provider %q — valid options are: anthropic, openai, ollama",
			cfg.Provider,
		)
	}
}
