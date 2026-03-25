package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
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

	logger  *slog.Logger
	tracer  AgentTracer
	metrics AgentMetrics
}

// Message is what the scheduler sends to an agent's Inbox.
type Message struct {
	// RunID is a unique identifier for this task run.
	// Used to namespace memory keys so parallel runs don't collide.
	RunID string

	// Input is what this agent should work on.
	Input string

	// Note: context is NOT carried here — it flows through the agent's
	// goroutine context set at Run() time, not through the message.
	// Passing contexts through structs is a Go anti-pattern.
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
	tracer AgentTracer,
	metrics AgentMetrics,
) *Agent {
	// Default to no-ops if not provided
	if tracer == nil {
		tracer = noopTracer{}
	}
	if metrics == nil {
		metrics = noopMetrics{}
	}
	return &Agent{
		cfg:      cfg,
		llm:      adapter,
		mem:      mem,
		registry: registry,
		Inbox:    make(chan Message, 1),
		output:   make(chan Result, 1),
		notify:   make(chan Result, 1),
		logger:   logger.With("agent_id", cfg.ID, "role", cfg.Role.String()),
		tracer:   tracer,
		metrics:  metrics,
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

func (a *Agent) Config() Config {
	return a.cfg
}

func (a *Agent) SetConfig(cfg Config) {
	a.cfg = cfg
}

// process handles a single Message — the thinking loop with retries.
// ctx comes from Run() — it carries the agent's lifetime and any
// timeout set by the supervisor. We apply an additional per-task
// timeout on top if cfg.Timeout is set.
func (a *Agent) process(ctx context.Context, msg Message) Result {
	start := time.Now()

	// Start an agent-level span — wraps the full process including retries
	ctx, finishSpan := a.tracer.StartAgent(ctx, a.cfg.ID, a.cfg.Role.String())

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

	maxAttempts := a.cfg.MaxRetries + 1
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			a.logger.Warn("retrying", "attempt", attempt, "error", lastErr)
			a.metrics.RecordAgentFailure(a.cfg.ID)
			histKey := memory.AgentKey(a.cfg.ID, "history")
			_ = a.mem.ClearHistory(ctx, histKey)
		}

		output, toolCalls, tokens, lastErr = a.think(ctx, msg)
		if lastErr == nil {
			break
		}
	}

	duration := time.Since(start)

	// Record agent-level metrics
	a.metrics.RecordAgentRun(a.cfg.ID, a.cfg.Role.String(), duration)
	a.metrics.RecordTokens(a.cfg.ID, a.llm.Provider(), tokens)

	// Close the agent span — mark as errored if all retries failed
	finishSpan(lastErr)

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

	// toolCallCounts tracks how many times each "tool:input" combination
	// has been called in this thinking attempt.
	// Key format: "toolName||inputJSON" — pipes chosen because they are
	// unlikely to appear in tool names.
	toolCallCounts := make(map[string]int)

	// maxDuplicateCalls is how many times the same tool+input pair is
	// allowed before we intervene. 2 allows one retry (sometimes useful
	// for transient failures) but blocks infinite loops.
	maxDuplicateCalls := a.cfg.MaxDuplicateToolCalls
	if maxDuplicateCalls == 0 {
		maxDuplicateCalls = 2
	}

	// maxTotalToolCalls is the absolute budget per thinking attempt.
	// Prevents runaway agents even when each individual tool varies slightly.
	maxTotalToolCalls := a.cfg.MaxTotalToolCalls
	if maxTotalToolCalls == 0 {
		maxTotalToolCalls = 20
	}

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

		// Start a span for this LLM call — closed when the call returns
		llmCtx, finishLLM := a.tracer.StartLLMCall(ctx, a.llm.Provider(), a.llm.Model())

		resp, err := a.llm.Complete(llmCtx, llm.Request{
			SystemPrompt: systemPrompt,
			History:      history,
			ToolSchemas:  allowedSchemas,
		})

		finishLLM(resp.Usage.Total(), err)

		if err != nil {
			return "", toolCallLog, totalTokens, fmt.Errorf("llm call: %w", err)
		}

		totalTokens += resp.Usage.Total()

		// ── LLM returned tool calls — execute them ───────────────
		if len(resp.ToolCalls) > 0 {

			// ── Total budget check ────────────────────────────────
			// Check before executing the batch. Count each call individually
			// against the budget — a batch of 3 costs 3 budget units.
			if len(toolCallLog)+len(resp.ToolCalls) > maxTotalToolCalls {
				a.logger.Warn("total tool call budget exceeded — redirecting LLM",
					"requested", len(resp.ToolCalls),
					"used", len(toolCallLog),
					"budget", maxTotalToolCalls,
				)
				_ = a.mem.Append(ctx, histKey, memory.Message{
					Role:      "user",
					Content:   fmt.Sprintf("You have used %d of your %d tool call budget. Stop calling tools and produce your final answer now using the information already gathered.", len(toolCallLog), maxTotalToolCalls),
					Timestamp: time.Now(),
				})
				continue
			}

			// ── Duplicate check — filter before executing ─────────
			// Build the list of calls that will actually run.
			// Calls that exceed their duplicate limit get a redirect
			// message instead — the batch continues without them.
			var toExecute []llm.ToolCallRequest
			var redirectMessages []string

			for _, tc := range resp.ToolCalls {
				callKey := tc.ToolName + "||" + tc.Input
				toolCallCounts[callKey]++
				count := toolCallCounts[callKey]

				if count > maxDuplicateCalls {
					a.logger.Warn("duplicate tool call in batch — skipping",
						"tool", tc.ToolName,
						"count", count,
					)
					redirectMessages = append(redirectMessages,
						fmt.Sprintf("You have already called %q with the same input %d times — do not call it again.", tc.ToolName, count-1),
					)
					continue
				}
				toExecute = append(toExecute, tc)
			}

			// If all calls in the batch were duplicates, inject redirect and continue
			if len(toExecute) == 0 {
				_ = a.mem.Append(ctx, histKey, memory.Message{
					Role:      "user",
					Content:   strings.Join(redirectMessages, " ") + " Use the results already in your history.",
					Timestamp: time.Now(),
				})
				continue
			}

			a.logger.Info("executing tool batch",
				"count", len(toExecute),
				"tools", toolNames(toExecute),
			)

			// ── Append assistant message with all tool_use blocks.
			assistantToolCalls := make([]memory.ToolCallRecord, 0, len(toExecute))
			for _, tc := range toExecute {
				assistantToolCalls = append(assistantToolCalls, memory.ToolCallRecord{
					ID:       tc.ID,
					ToolName: tc.ToolName,
					Input:    tc.Input,
				})
			}
			if err := a.mem.Append(ctx, histKey, memory.Message{
				Role:      "assistant",
				Timestamp: time.Now(),
				ToolCalls: assistantToolCalls,
			}); err != nil {
				return "", toolCallLog, totalTokens, fmt.Errorf("append tool batch request: %w", err)
			}

			// ── Execute all tools concurrently ────────────────────
			type toolResult struct {
				tc     llm.ToolCallRequest
				output json.RawMessage
				err    error
				dur    time.Duration
			}

			results := make([]toolResult, len(toExecute))
			var wg sync.WaitGroup

			for i, tc := range toExecute {
				wg.Add(1)
				go func(i int, tc llm.ToolCallRequest) {
					defer wg.Done()
					start := time.Now()
					toolCtx, finishTool := a.tracer.StartToolCall(ctx, tc.ToolName, tc.Input)
					out, execErr := a.registry.Execute(toolCtx, tc.ToolName, json.RawMessage(tc.Input))
					duration := time.Since(start)
					finishTool(string(out), execErr)
					a.metrics.RecordToolCall(tc.ToolName, duration, execErr)
					results[i] = toolResult{tc: tc, output: out, err: execErr, dur: duration}
				}(i, tc)
			}

			wg.Wait()

			// ── Append one tool_result message per result ─────────
			// Then append to toolCallLog. Order matches toExecute.
			for _, r := range results {
				outputStr := string(r.output)
				logEntry := tools.ToolCall{
					ToolName: r.tc.ToolName,
					Input:    r.tc.Input,
					Duration: r.dur,
				}

				if r.err != nil {
					a.logger.Warn("tool call failed", "tool", r.tc.ToolName, "error", r.err)
					logEntry.Error = r.err
					r.output = json.RawMessage(fmt.Sprintf(`{"error":%q}`, r.err.Error()))
					outputStr = string(r.output)
				} else {
					logEntry.Output = outputStr
				}

				toolCallLog = append(toolCallLog, logEntry)

				if err := a.mem.Append(ctx, histKey, memory.Message{
					Role:      "user",
					Timestamp: time.Now(),
					ToolCall: &memory.ToolCallRecord{
						ID:       r.tc.ID,
						ToolName: r.tc.ToolName,
						Input:    r.tc.Input,
						Output:   outputStr,
						Error:    errorString(r.err),
					},
				}); err != nil {
					return "", toolCallLog, totalTokens, fmt.Errorf("append tool result: %w", err)
				}
			}

			// Inject any duplicate redirect messages after the results
			for _, msg := range redirectMessages {
				_ = a.mem.Append(ctx, histKey, memory.Message{
					Role:      "user",
					Content:   msg,
					Timestamp: time.Now(),
				})
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

// toolNames returns a comma-separated list of tool names for logging.
func toolNames(calls []llm.ToolCallRequest) string {
	names := make([]string, len(calls))
	for i, tc := range calls {
		names[i] = tc.ToolName
	}
	return strings.Join(names, ", ")
}
