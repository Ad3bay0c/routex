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

// Agent is a single AI worker — one goroutine with one LLM, one memory
// scope, and one set of tools.
//
// Communication model:
//
//	Inbox   ← scheduler sends work here to start the agent
//	output  → scheduler reads results here (one reader only)
//	notify  → supervisor reads results here (one reader only)
//
// An Agent's life:
//  1. Runtime creates the Agent and calls Run() in a new goroutine
//  2. Agent waits on its Inbox channel for a Message
//  3. Message arrives — agent starts the thinking loop
//  4. Thinking loop: call LLM → maybe execute tool → call LLM again → repeat
//  5. LLM produces final text — agent sends result to both output and notify channels
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

	// Inbox receives work from the scheduler.
	// Buffered(1) so the scheduler can send without blocking
	// even if the agent is still finishing its previous task.
	Inbox chan Message

	// output delivers results to the scheduler.
	// Scheduler is the only reader — do not read from this directly.
	output chan Result

	// notify delivers the same results to the supervisor.
	// Supervisor is the only reader — do not read from this directly.
	notify chan Result

	logger *slog.Logger
}

// Message is what the scheduler sends to an agent's Inbox.
type Message struct {
	// RunID is a unique identifier for this task run.
	// Used to namespace memory keys so parallel runs don't collide.
	RunID string

	// Input is what this agent should work on.
	Input string
}

// Result is what the agent sends when it finishes processing a Message.
type Result struct {
	// AgentID identifies which agent produced this result.
	AgentID string

	// Output is the final text the agent produced.
	Output string

	// ToolCalls is a log of every tool execution during this run.
	ToolCalls []tools.ToolCall

	// TokensUsed is the total tokens consumed across all LLM calls.
	TokensUsed int

	// StartedAt is when the agent started actual processing.
	StartedAt time.Time

	// Err is non-nil if the agent failed even after all retries.
	Err error
}

// New creates an Agent from its config and dependencies.
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
		notify:   make(chan Result, 1),
		logger:   logger.With("agent_id", cfg.ID, "role", cfg.Role.String()),
	}
}

// Run is the agent's goroutine — started by the supervisor.
// It waits for messages, processes them, and broadcasts results
// to both the scheduler (output) and the supervisor (notify).
// Exits cleanly when ctx is cancelled.
func (a *Agent) Run(ctx context.Context) {
	a.logger.Info("agent started")

	for {
		select {
		case <-ctx.Done():
			a.logger.Info("agent stopping", "reason", ctx.Err())
			return

		case msg := <-a.Inbox:
			a.logger.Info("received task", "run_id", msg.RunID)
			result := a.process(ctx, msg)

			// Broadcast the result to both readers.
			// Both channels are buffered(1) so neither send blocks
			// as long as readers consume promptly.
			a.output <- result
			a.notify <- result
		}
	}
}

// Output returns the channel the scheduler reads results from.
// Only the scheduler should read from this channel.
func (a *Agent) Output() <-chan Result {
	return a.output
}

// Notify returns the channel the supervisor reads results from.
// Only the supervisor should read from this channel.
func (a *Agent) Notify() <-chan Result {
	return a.notify
}

// ID returns the agent's unique identifier.
func (a *Agent) ID() string { return a.cfg.ID }

// DependsOn returns the IDs of agents that must finish before this one starts.
func (a *Agent) DependsOn() []string {
	return a.cfg.DependsOn
}

// process handles a single Message — the thinking loop with retries.
// ctx comes from Run() — it carries the agent's lifetime and any
// timeout set by the supervisor. We apply an additional per-task
// timeout on top if cfg.Timeout is set.
func (a *Agent) process(ctx context.Context, msg Message) Result {
	start := time.Now()

	// Apply the agent's per-task timeout on top of the goroutine context.
	// If the goroutine context is cancelled first, that wins.
	if a.cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, a.cfg.Timeout)
		defer cancel()
	}

	var (
		output    string
		toolCalls []tools.ToolCall
		tokens    int
		lastErr   error
	)

	// Retry loop — each attempt clears history for a clean slate.
	maxAttempts := a.cfg.MaxRetries + 1
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			a.logger.Warn("retrying", "attempt", attempt, "error", lastErr)
			histKey := memory.AgentKey(a.cfg.ID, "history")
			_ = a.mem.ClearHistory(ctx, histKey)
		}

		output, toolCalls, tokens, lastErr = a.think(ctx, msg)
		if lastErr == nil {
			break
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
		StartedAt:  start,
		Err:        lastErr,
	}
}

// think is the core LLM loop for a single attempt.
// It calls the LLM, handles tool calls, builds up history,
// and returns when the LLM produces a final text response.
func (a *Agent) think(ctx context.Context, msg Message) (string, []tools.ToolCall, int, error) {
	histKey := memory.AgentKey(a.cfg.ID, "history")
	var toolCallLog []tools.ToolCall
	totalTokens := 0

	systemPrompt := fmt.Sprintf("%s\n\nYour specific goal for this task: %s",
		a.cfg.Role.SystemPrompt(),
		a.cfg.Goal,
	)

	if err := a.mem.Append(ctx, histKey, memory.Message{
		Role:      "user",
		Content:   msg.Input,
		Timestamp: time.Now(),
	}); err != nil {
		return "", nil, 0, fmt.Errorf("seed history: %w", err)
	}

	allowedSchemas := a.allowedToolSchemas()

	// The thinking loop — keeps running until the LLM gives a text response
	// or the context is cancelled (timeout / shutdown).
	for {
		select {
		case <-ctx.Done():
			return "", toolCallLog, totalTokens, ctx.Err()
		default:
		}

		history, err := a.mem.History(ctx, histKey, 0)
		if err != nil {
			return "", toolCallLog, totalTokens, fmt.Errorf("retrieve history: %w", err)
		}

		a.logger.Debug("calling llm", "history_len", len(history), "tools", len(allowedSchemas))

		resp, err := a.llm.Complete(ctx, llm.Request{
			SystemPrompt: systemPrompt,
			History:      history,
			ToolSchemas:  allowedSchemas,
		})
		if err != nil {
			return "", toolCallLog, totalTokens, fmt.Errorf("llm call: %w", err)
		}

		totalTokens += resp.Usage.Total()

		// ── LLM wants to call a tool ──────────────────────────────
		if resp.ToolCall != nil {
			tc := resp.ToolCall

			a.logger.Info("tool call requested", "tool", tc.ToolName, "input", tc.Input)

			if err := a.mem.Append(ctx, histKey, memory.Message{
				Role:      "assistant",
				Timestamp: time.Now(),
				ToolCall: &memory.ToolCallRecord{
					ToolName: tc.ToolName,
					Input:    tc.Input,
				},
			}); err != nil {
				return "", toolCallLog, totalTokens, fmt.Errorf("append tool request: %w", err)
			}

			toolStart := time.Now()
			output, toolErr := a.registry.Execute(ctx, tc.ToolName, json.RawMessage(tc.Input))

			toolResult := tools.ToolCall{
				ToolName: tc.ToolName,
				Input:    tc.Input,
				Duration: time.Since(toolStart),
			}

			if toolErr != nil {
				a.logger.Warn("tool call failed", "tool", tc.ToolName, "error", toolErr)
				toolResult.Error = toolErr
				output = json.RawMessage(fmt.Sprintf(`{"error":%q}`, toolErr.Error()))
			} else {
				toolResult.Output = string(output)
			}

			toolCallLog = append(toolCallLog, toolResult)

			if err := a.mem.Append(ctx, histKey, memory.Message{
				Role:      "user",
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

			continue
		}

		// ── LLM produced a text response — done ──────────────────
		if resp.Content != "" {
			a.logger.Info("agent finished", "tokens", totalTokens, "tool_calls", len(toolCallLog))

			if err := a.mem.Append(ctx, histKey, memory.Message{
				Role:      "assistant",
				Content:   resp.Content,
				Timestamp: time.Now(),
			}); err != nil {
				a.logger.Warn("failed to append final response to history", "error", err)
			}

			outputKey := memory.AgentKey(a.cfg.ID, "output")
			if err := a.mem.Set(ctx, outputKey, resp.Content, 0); err != nil {
				a.logger.Warn("failed to save output to memory", "error", err)
			}

			return resp.Content, toolCallLog, totalTokens, nil
		}

		// ── empty response — should not happen but handle it ─────
		return "", toolCallLog, totalTokens, fmt.Errorf(
			"llm returned empty response (finish_reason: %s)", resp.FinishReason,
		)
	}
}

// allowedToolSchemas returns the schemas for only the tools
// this agent is configured to use.
func (a *Agent) allowedToolSchemas() map[string]tools.Schema {
	schemas := make(map[string]tools.Schema, len(a.cfg.Tools))
	for _, name := range a.cfg.Tools {
		t, ok := a.registry.Get(name)
		if !ok {
			a.logger.Warn("tool listed in config but not registered", "tool", name)
			continue
		}
		schemas[name] = t.Schema()
	}
	return schemas
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
