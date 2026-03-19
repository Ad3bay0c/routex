package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/Ad3bay0c/routex/llm"
	"github.com/Ad3bay0c/routex/memory"
	"github.com/Ad3bay0c/routex/tools"
)

// Agent is a single AI worker — one goroutine, one LLM connection,
// one memory scope, one set of allowed tools.
//
// Agents never share memory directly. They communicate exclusively
// through channels — the Inbox receives work, the output channel
// sends results back to the runtime. No shared state, no mutexes
// between agents. This is the Go concurrency model applied to AI.
//
// An Agent's life:
//  1. Runtime creates the Agent and calls Run() in a new goroutine
//  2. Agent waits on its Inbox channel for a Message
//  3. Message arrives — agent starts the thinking loop
//  4. Thinking loop: call LLM → maybe execute tool → call LLM again → repeat
//  5. LLM produces final text — agent sends result to output channel
//  6. Agent goes back to step 2, waiting for the next message
//  7. When ctx is cancelled — agent cleans up and exits
type Agent struct {
	// cfg holds everything set in agents.yaml for this agent —
	// ID, Role, Goal, Tools, DependsOn, MaxRetries, Timeout, Restart
	cfg Config

	// llm is the language model this agent thinks with.
	// Could be Anthropic, OpenAI, or Ollama — the agent does not care.
	// It only knows the Adapter interface.
	llm llm.Adapter

	// mem is this agent's memory store.
	// Shared with other agents in the crew but namespaced by agent ID
	// so agents cannot accidentally read each other's working memory.
	mem memory.Store

	// registry holds all tools registered with the runtime.
	// The agent only uses tools listed in cfg.Tools — the registry
	// enforces this by only returning schemas for allowed tools.
	registry *tools.Registry

	// Inbox is the channel the runtime uses to send work to this agent.
	// Buffered with capacity 1 — the runtime can send one message
	// without blocking even if the agent is busy finishing its last task.
	Inbox chan Message

	// output is where this agent sends its result when done.
	// The runtime reads from this channel after sending to Inbox.
	output chan Result

	// logger is scoped to this agent — every log line includes the agent ID.
	logger *slog.Logger
}

// Message is what the runtime sends to an agent's Inbox to start work.
// It carries the task and the context for this specific run.
type Message struct {
	// RunID is a unique identifier for this task run.
	// Used to namespace memory keys so parallel runs don't collide.
	RunID string

	// Input is what this agent should work on.
	// For the planner: the raw user task.
	// For the writer: the planner's output (the plan).
	// For the critic: the writer's output (the draft).
	Input string

	// ctx carries the deadline and cancellation for this run.
	// When the runtime cancels ctx — timeout, user interrupt, upstream failure —
	// the agent's LLM calls and tool executions are cancelled too.
	ctx context.Context
}

// Result is what the agent sends back through its output channel when done.
type Result struct {
	// AgentID identifies which agent produced this result.
	AgentID string

	// Output is the final text the agent produced.
	Output string

	// ToolCalls is a log of every tool execution during this run.
	ToolCalls []tools.ToolCall

	// TokensUsed is the total tokens consumed across all LLM calls.
	TokensUsed int

	// Duration is how long this agent ran.
	Duration time.Time

	// Err is non-nil if the agent failed even after all retries.
	Err error
}

// New creates an Agent from its config and dependencies.
// Called by the runtime for each agent defined in agents.yaml.
// Dependencies (llm, mem, registry) are injected — the agent
// does not create them itself. This makes agents easy to test.
func New(
	cfg Config,
	adapter llm.Adapter,
	mem memory.Store,
	registry *tools.Registry,
	logger *slog.Logger,
) *Agent {
	return &Agent{
		cfg:      cfg,
		llm:      adapter,
		mem:      mem,
		registry: registry,
		Inbox:    make(chan Message, 1),
		output:   make(chan Result, 1),
		logger:   logger.With("agent_id", cfg.ID, "role", cfg.Role.String()),
	}
}

// Run is the agent's main goroutine — started by the runtime with go agent.Run().
// It loops forever: wait for a message, do the work, send the result, repeat.
// When ctx is cancelled, the loop exits and the output channel is closed —
// this signals the supervisor's fan-in forwarder that this agent is done.
func (a *Agent) Run(ctx context.Context) {
	a.logger.Info("agent started")
	defer close(a.output) // signal to supervisor fan-in that we are done

	for {
		select {
		case <-ctx.Done():
			// Runtime is shutting down — exit cleanly
			a.logger.Info("agent stopping", "reason", ctx.Err())
			return

		case msg := <-a.Inbox:
			// Work arrived — process it
			a.logger.Info("received task", "run_id", msg.RunID)
			result := a.process(msg)
			a.output <- result
		}
	}
}

// Output returns the channel the runtime reads results from.
// Kept private — only the runtime receives results, not other agents.
func (a *Agent) Output() <-chan Result {
	return a.output
}

// ID returns the agent's unique identifier.
func (a *Agent) ID() string {
	return a.cfg.ID
}

// DependsOn returns the IDs of agents that must finish before this one starts.
func (a *Agent) DependsOn() []string {
	return a.cfg.DependsOn
}

// process handles a single Message — this is the thinking loop.
// It is called from Run() and blocks until the agent is done or fails.
// Retries are handled here — if the loop fails, we retry up to MaxRetries times.
func (a *Agent) process(msg Message) Result {
	start := time.Now()

	// Apply the agent's timeout on top of the run context.
	// If agent timeout is 60s and the run has 4 minutes left,
	// this agent will be cancelled after 60s regardless.
	ctx := msg.ctx
	if a.cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(msg.ctx, a.cfg.Timeout)
		defer cancel()
	}

	var (
		output    string
		toolCalls []tools.ToolCall
		tokens    int
		lastErr   error
	)

	// Retry loop — attempt the thinking loop up to MaxRetries+1 times.
	// On each attempt we clear the agent's history and start fresh.
	// This means a crashed attempt does not pollute the next attempt.
	maxAttempts := a.cfg.MaxRetries + 1
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			a.logger.Warn("retrying", "attempt", attempt, "error", lastErr)
			// Clear history from the failed attempt so the LLM
			// starts with a clean slate rather than a confused partial conversation
			histKey := memory.AgentKey(a.cfg.ID, "history")
			_ = a.mem.ClearHistory(ctx, histKey)
		}

		output, toolCalls, tokens, lastErr = a.think(ctx, msg)
		if lastErr == nil {
			break // success — exit the retry loop
		}
	}

	if lastErr != nil {
		a.logger.Error("agent failed after all retries", "error", lastErr)
	}

	return Result{
		AgentID:    a.cfg.ID,
		Output:     output,
		ToolCalls:  toolCalls,
		TokensUsed: tokens,
		Duration:   start,
		Err:        lastErr,
	}
}

// think is the core thinking loop for a single attempt.
// It calls the LLM, handles tool calls, builds up history,
// and returns when the LLM produces a final text response.
//
// This is the loop described in llm/adapter.go:
//
//	call Complete() → tool call? → execute → append result → repeat
//	call Complete() → text response? → done
func (a *Agent) think(ctx context.Context, msg Message) (string, []tools.ToolCall, int, error) {
	histKey := memory.AgentKey(a.cfg.ID, "history")
	var toolCallLog []tools.ToolCall
	totalTokens := 0

	// Build the system prompt: role identity + specific goal
	systemPrompt := fmt.Sprintf("%s\n\nYour specific goal for this task: %s",
		a.cfg.Role.SystemPrompt(),
		a.cfg.Goal,
	)

	// Seed the history with the incoming task as the first user message
	if err := a.mem.Append(ctx, histKey, memory.Message{
		Role:      "user",
		Content:   msg.Input,
		Timestamp: time.Now(),
	}); err != nil {
		return "", nil, 0, fmt.Errorf("seed history: %w", err)
	}

	// Get the schemas for only the tools this agent is allowed to use
	allowedSchemas := a.allowedToolSchemas()

	// The thinking loop — keeps running until the LLM gives us a text response
	// or the context is cancelled (timeout / shutdown)
	for {
		// Check for cancellation before each LLM call
		// This catches timeouts that occur between iterations
		select {
		case <-ctx.Done():
			return "", toolCallLog, totalTokens, ctx.Err()
		default:
		}

		// Retrieve full conversation history to send to the LLM
		history, err := a.mem.History(ctx, histKey, 0)
		if err != nil {
			return "", toolCallLog, totalTokens, fmt.Errorf("retrieve history: %w", err)
		}

		// Call the LLM with everything it needs
		a.logger.Debug("calling llm", "history_len", len(history), "tools", len(allowedSchemas))

		resp, err := a.llm.Complete(ctx, llm.Request{
			SystemPrompt: systemPrompt,
			History:      history,
			ToolSchemas:  allowedSchemas,
		})
		if err != nil {
			return "", toolCallLog, totalTokens, fmt.Errorf("llm call: %w", err)
		}

		// Accumulate token usage across all turns
		totalTokens += resp.Usage.Total()

		// ─────────── LLM wants to call a tool ────────────────────────────
		if resp.ToolCall != nil {
			tc := resp.ToolCall

			a.logger.Info("tool call requested",
				"tool", tc.ToolName,
				"input", tc.Input,
			)

			// Record the LLM's tool call request in history as an assistant message
			if err := a.mem.Append(ctx, histKey, memory.Message{
				Role:      "assistant",
				Content:   "",
				Timestamp: time.Now(),
				ToolCall: &memory.ToolCallRecord{
					ToolName: tc.ToolName,
					Input:    tc.Input,
				},
			}); err != nil {
				return "", toolCallLog, totalTokens, fmt.Errorf("append tool request: %w", err)
			}

			// Execute the tool
			toolStart := time.Now()
			output, toolErr := a.registry.Execute(ctx, tc.ToolName, json.RawMessage(tc.Input))

			toolResult := tools.ToolCall{
				ToolName: tc.ToolName,
				Input:    tc.Input,
				Duration: time.Since(toolStart),
			}

			if toolErr != nil {
				// Tool failed — tell the LLM what went wrong
				// It may decide to try a different tool or approach
				a.logger.Warn("tool call failed", "tool", tc.ToolName, "error", toolErr)
				toolResult.Error = toolErr
				output = json.RawMessage(fmt.Sprintf(`{"error": %q}`, toolErr.Error()))
			} else {
				toolResult.Output = string(output)
				a.logger.Info("tool call succeeded", "tool", tc.ToolName)
			}

			toolCallLog = append(toolCallLog, toolResult)

			// Append the tool result to history as a user message
			// The LLM will read this on the next iteration and continue thinking
			if err := a.mem.Append(ctx, histKey, memory.Message{
				Role:      "user",
				Content:   "",
				Timestamp: time.Now(),
				ToolCall: &memory.ToolCallRecord{
					ToolName: tc.ToolName,
					Input:    tc.Input,
					Output:   string(output),
					Error:    errorString(toolErr),
				},
			}); err != nil {
				return "", toolCallLog, totalTokens, fmt.Errorf("append tool result: %w", err)
			}

			// Loop back — call the LLM again with the tool result in history
			continue
		}

		// ──────────── LLM produced a text response — we are done ──────────
		if resp.Content != "" {
			a.logger.Info("agent finished", "tokens", totalTokens, "tool_calls", len(toolCallLog))

			// Append the final response to history so downstream agents
			// that read this agent's memory see the complete conversation
			if err := a.mem.Append(ctx, histKey, memory.Message{
				Role:      "assistant",
				Content:   resp.Content,
				Timestamp: time.Now(),
			}); err != nil {
				// Non-fatal — we have the output, just couldn't persist it
				a.logger.Warn("failed to append final response to history", "error", err)
			}

			// Save the final output under a stable key so downstream
			// agents can retrieve it without reading the full history
			outputKey := memory.AgentKey(a.cfg.ID, "output")
			if err := a.mem.Set(ctx, outputKey, resp.Content, 0); err != nil {
				a.logger.Warn("failed to save output to memory", "error", err)
			}

			return resp.Content, toolCallLog, totalTokens, nil
		}

		// ── Case 3: empty response — should not happen but handle it ────
		// Some models return empty content with finish_reason "stop".
		// Treat it as an error so the retry logic can kick in.
		return "", toolCallLog, totalTokens, fmt.Errorf(
			"llm returned empty response (finish_reason: %s)", resp.FinishReason,
		)
	}
}

// allowedToolSchemas returns the schemas for only the tools
// this agent is configured to use.
//
// Even if 10 tools are registered with the runtime, a planner
// that is only allowed ["web_search"] will only see web_search's schema.
// This limits what the LLM can request and reduces prompt token count.
func (a *Agent) allowedToolSchemas() map[string]tools.Schema {
	schemas := make(map[string]tools.Schema, len(a.cfg.Tools))
	for _, name := range a.cfg.Tools {
		t, ok := a.registry.Get(name)
		if !ok {
			// Tool listed in config but not registered — log and skip.
			// config.go validates this at startup so it should not happen,
			// but defensive code here prevents a silent capability gap.
			a.logger.Warn("tool listed in config but not registered", "tool", name)
			continue
		}
		schemas[name] = t.Schema()
	}
	return schemas
}

// errorString converts an error to its string representation.
// Returns empty string if err is nil — safe to use in struct fields.
func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
