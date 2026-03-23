package routex

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/google/uuid"

	"github.com/Ad3bay0c/routex/agents"
	"github.com/Ad3bay0c/routex/internal/scheduler"
	"github.com/Ad3bay0c/routex/internal/supervisor"
	"github.com/Ad3bay0c/routex/llm"
	"github.com/Ad3bay0c/routex/memory"
	"github.com/Ad3bay0c/routex/tools"

	// Blank imports trigger each sub-package's init() functions,
	// registering their built-in tool factories automatically.
	// Add a new line here whenever a new tool sub-package is created.
	_ "github.com/Ad3bay0c/routex/tools/ai"
	_ "github.com/Ad3bay0c/routex/tools/comms"
	_ "github.com/Ad3bay0c/routex/tools/file"
	_ "github.com/Ad3bay0c/routex/tools/search"
	_ "github.com/Ad3bay0c/routex/tools/web"
)

// Runtime is the heart of Routex.
// It owns all agents, the tool registry, the memory store,
// the scheduler, and the supervisor. Users interact with
// Routex entirely through Runtime's methods.
//
// There are two ways to get a Runtime:
//
//  1. From a YAML file:
//     rt, err := routex.LoadConfig("agents.yaml")
//
//  2. Programmatically:
//     rt := routex.NewRuntime(routex.Config{...})
//
// Both paths produce the same Runtime — just different entry points.
type Runtime struct {
	cfg        Config
	registry   *tools.Registry
	mem        memory.Store
	adapter    llm.Adapter
	agentList  []*agents.Agent
	supervisor *supervisor.Supervisor
	scheduler  *scheduler.Scheduler
	logger     *slog.Logger
	task       Task
	started    bool
}

// NewRuntime creates a Runtime from a validated Config.
// Called by LoadConfig() after parsing YAML, or directly
// when using the programmatic API.
//
// NewRuntime does not start any goroutines — call Start() or
// StartAndRun() when you are ready to run.
func NewRuntime(cfg Config) (*Runtime, error) {
	// Set up structured logger
	logLevel := parseLogLevel(cfg.LogLevel)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: logLevel,
	})).With("runtime", cfg.Name)

	// Build the memory store from config
	mem, err := buildMemoryStore(cfg.Memory)
	if err != nil {
		return nil, fmt.Errorf("routex: build memory store: %w", err)
	}

	// Build the LLM adapter from config
	adapter, err := llm.New(cfg.LLM)
	if err != nil {
		return nil, fmt.Errorf("routex: build llm adapter: %w", err)
	}

	logger.Info("runtime created",
		"provider", cfg.LLM.Provider,
		"model", cfg.LLM.Model,
		"memory", cfg.Memory.Backend,
	)

	return &Runtime{
		cfg:      cfg,
		registry: tools.NewRegistry(),
		mem:      mem,
		adapter:  adapter,
		logger:   logger,
		task:     cfg.Task,
	}, nil
}

// RegisterTool makes a tool available to agents.
// Must be called before Start() or StartAndRun().
// Tools listed in agents.yaml but not registered here will be
// silently skipped by agents — register all tools you intend to use.
//
// Usage:
//
//	rt.RegisterTool(tools.WebSearch())
//	rt.RegisterTool(tools.WriteFile())
//	rt.RegisterTool(&MyCustomTool{})
func (rt *Runtime) RegisterTool(t tools.Tool) {
	rt.registry.Register(t)
	rt.logger.Info("tool registered", "name", t.Name())
}

// AddAgent adds an agent to the runtime programmatically.
// Use this when building your runtime in Go code instead of YAML.
// Must be called before Start() or StartAndRun().
//
// Usage:
//
//	rt.AddAgent(agents.Agent{
//	    ID:   "planner",
//	    Role: agents.Planner,
//	    Goal: "Break the task into clear steps",
//	})
func (rt *Runtime) AddAgent(cfg agents.Config) {
	agent := agents.New(cfg, rt.adapter, rt.mem, rt.registry, rt.logger)
	rt.agentList = append(rt.agentList, agent)
	rt.logger.Info("agent added", "id", cfg.ID, "role", cfg.Role.String())
}

// SetTask overrides the task that will run when StartAndRun() is called.
// Use this to provide a task from a dynamic source — HTTP request,
// message queue, database — instead of hardcoding it in YAML.
//
// Usage:
//
//	rt.SetTask(routex.Task{
//	    Input: r.FormValue("topic"),  // from an HTTP request
//	})
func (rt *Runtime) SetTask(t Task) {
	rt.task = t
	rt.logger.Debug("task set", "input_len", len(t.Input))
}

// autoRegisterTools walks the ToolConfigs from the YAML and tries to
// instantiate each one from the built-in registry.
// Tools already manually registered via RegisterTool() are skipped —
// manual registration always wins over auto-discovery.
// Returns an error only if a tool is listed in the YAML but is neither
// a built-in nor manually registered — that is always a configuration mistake.
func (rt *Runtime) autoRegisterTools() error {
	for _, cfg := range rt.cfg.ToolConfigs {
		// Already registered manually — skip, manual wins
		if _, ok := rt.registry.Get(cfg.Name); ok {
			rt.logger.Debug("tool already registered manually, skipping auto-discovery",
				"tool", cfg.Name,
			)
			continue
		}

		// Try to resolve from built-in registry
		tool, err := tools.Resolve(cfg.Name, cfg)
		if err != nil {
			// Not a built-in — check if it was manually registered
			// under a different timing (e.g. registered after LoadConfig)
			var notBuiltin tools.ErrToolNotBuiltin
			if errors.As(err, &notBuiltin) {
				// Not an error yet — the tool might be registered manually
				// before Start() is called. We will catch missing tools
				// inside allowedToolSchemas() when agents actually run.
				rt.logger.Warn("tool listed in YAML is not a built-in — register it manually with rt.RegisterTool()",
					"tool", cfg.Name,
				)
				continue
			}
			return fmt.Errorf("auto-register tool %q: %w", cfg.Name, err)
		}

		rt.registry.Register(tool)
		rt.logger.Info("tool auto-registered from built-in registry", "tool", cfg.Name)
	}
	return nil
}

// Start initialises the supervisor and scheduler, builds all agents
// from the config, and launches their goroutines.
// Returns an error if the agent graph is invalid (cycle, missing reference).
//
// After Start(), use Run() to send a task.
// Or skip Start() entirely and call StartAndRun() which does both.
func (rt *Runtime) Start(ctx context.Context) error {
	if rt.started {
		return fmt.Errorf("routex: runtime already started")
	}

	// Auto-register built-in tools listed in the YAML config.
	// Must happen before agents are built so they can find their tools.
	// Manually registered tools (via RegisterTool) are never overwritten.
	if err := rt.autoRegisterTools(); err != nil {
		return fmt.Errorf("routex: auto-register tools: %w", err)
	}

	// Build agents from config if none were added programmatically
	if len(rt.agentList) == 0 {
		for _, cfg := range rt.cfg.Agents {
			agent := agents.New(cfg, rt.adapter, rt.mem, rt.registry, rt.logger)
			rt.agentList = append(rt.agentList, agent)
		}
	}

	if len(rt.agentList) == 0 {
		return fmt.Errorf("routex: no agents configured — add agents via YAML or AddAgent()")
	}

	// Build the supervisor first — scheduler needs a reference to it
	// so it can report failures and wait for restart decisions.
	rt.supervisor = supervisor.New(
		rt.agentList,
		agents.OneForOne, // default policy
		3,                // max restarts per agent
		time.Minute,      // restart window
		rt.logger,
	)

	// Build the scheduler — passes the supervisor so the two cooperate on failures
	rt.scheduler = scheduler.New(rt.agentList, rt.supervisor, rt.logger)

	// Launch the supervisor — starts all agent goroutines
	rt.supervisor.Start(ctx)

	rt.started = true
	rt.logger.Info("runtime started", "agents", len(rt.agentList))

	return nil
}

// Run dispatches a Task to the agent crew and waits for all agents
// to complete. Returns the final Result.
//
// The scheduler determines agent execution order based on depends_on.
// Agents without dependencies run immediately. Agents with dependencies
// wait until their upstream agents have successfully completed.
//
// Run blocks until all agents finish or ctx is cancelled.
func (rt *Runtime) Run(ctx context.Context, task Task) (Result, error) {
	if !rt.started {
		return Result{}, fmt.Errorf("routex: call Start() before Run()")
	}

	// Generate a unique run ID for this task execution.
	// Used to namespace memory keys so parallel runs don't collide.
	runID := uuid.New().String()
	ctx = scheduler.WithRunID(ctx, runID)

	rt.logger.Info("run starting",
		"run_id", runID,
		"input_len", len(task.Input),
	)

	startTime := time.Now()

	// Apply task-level timeout if set
	if task.MaxDuration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, task.MaxDuration)
		defer cancel()
	}

	// Hand the task to the scheduler — it drives the whole execution
	agentResults, err := rt.scheduler.Run(ctx, task.Input)
	if err != nil {
		return Result{
			Error:    err,
			Duration: time.Since(startTime),
			TraceID:  runID,
		}, err
	}

	// Assemble the final Result from all agent results
	result := rt.assembleResult(runID, startTime, agentResults)

	// Write output to file if configured
	if task.OutputFile != "" {
		if writeErr := os.WriteFile(task.OutputFile, []byte(result.Output), 0644); writeErr != nil {
			rt.logger.Warn("failed to write output file",
				"path", task.OutputFile,
				"error", writeErr,
			)
		} else {
			rt.logger.Info("output written", "path", task.OutputFile)
		}
	}

	rt.logger.Info("run complete",
		"run_id", runID,
		"duration", time.Since(startTime),
		"tokens", result.TokensUsed,
	)

	return result, nil
}

// StartAndRun is a convenience method that calls Start() then Run()
// using the task configured in agents.yaml (or set via SetTask()).
//
// This is the method most YAML-driven applications call — one line
// from config to result.
//
// Usage:
//
//	result, err := rt.StartAndRun(ctx)
func (rt *Runtime) StartAndRun(ctx context.Context) (Result, error) {
	if err := rt.Start(ctx); err != nil {
		return Result{}, err
	}
	return rt.Run(ctx, rt.task)
}

// Stop gracefully shuts down the runtime.
// Cancels all running agent goroutines and closes the memory store.
// Always call Stop() when you are done — even in tests.
//
// After Stop(), the Runtime cannot be restarted. Create a new one.
func (rt *Runtime) Stop() {
	rt.logger.Info("runtime stopping")

	// Close the memory store — flushes any pending writes,
	// closes Redis connections, frees resources
	if rt.mem != nil {
		if err := rt.mem.Close(); err != nil {
			rt.logger.Warn("error closing memory store", "error", err)
		}
	}

	rt.started = false
	rt.logger.Info("runtime stopped")
}

// assembleResult builds the final Result from individual agent results.
// It finds the output of the last agent in the execution chain,
// sums up all token usage, and converts internal types to public types.
func (rt *Runtime) assembleResult(
	runID string,
	startTime time.Time,
	agentResults map[string]agents.Result,
) Result {
	result := Result{
		AgentResults: make(map[string]AgentResult, len(agentResults)),
		TraceID:      runID,
		Duration:     time.Since(startTime),
	}

	// Convert each agents.Result into the public AgentResult type
	// and find the last agent's output to use as the final output
	for id, ar := range agentResults {
		// Convert tools.ToolCall slice to routex.ToolCall slice
		toolCalls := make([]ToolCall, len(ar.ToolCalls))
		for i, tc := range ar.ToolCalls {
			toolCalls[i] = ToolCall{
				ToolName: tc.ToolName,
				Input:    tc.Input,
				Output:   tc.Output,
				Duration: tc.Duration,
				Error:    tc.Error,
			}
		}

		result.AgentResults[id] = AgentResult{
			AgentID:    id,
			Output:     ar.Output,
			ToolCalls:  toolCalls,
			TokensUsed: ar.TokensUsed,
			Duration:   time.Since(ar.StartedAt),
			Error:      ar.Err,
		}

		result.TokensUsed += ar.TokensUsed
	}

	// The final output is the output of the last agent in the chain.
	// We find it by looking for the agent with no other agent depending on it.
	result.Output = rt.findFinalOutput(agentResults)

	return result
}

// findFinalOutput finds the output of the terminal agent in the crew —
// the agent that no other agent depends on. In a linear chain
// planner → writer → critic, the critic is the terminal agent.
func (rt *Runtime) findFinalOutput(agentResults map[string]agents.Result) string {
	// Build a set of all agent IDs that are depended upon by someone
	hasDependents := make(map[string]bool)
	for _, a := range rt.agentList {
		for _, dep := range a.DependsOn() {
			hasDependents[dep] = true
		}
	}

	// The terminal agent is one that nobody depends on
	// In most crews there is exactly one — the last in the chain
	for _, a := range rt.agentList {
		if !hasDependents[a.ID()] {
			if r, ok := agentResults[a.ID()]; ok {
				return r.Output
			}
		}
	}

	return ""
}

// parseLogLevel converts a log level string to a slog.Level.
// Defaults to Info for unknown values.
func parseLogLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
