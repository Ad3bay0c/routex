package routex

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Ad3bay0c/routex/agents"
	"github.com/Ad3bay0c/routex/internal/scheduler"
	"github.com/Ad3bay0c/routex/internal/supervisor"
	"github.com/Ad3bay0c/routex/llm"
	"github.com/Ad3bay0c/routex/memory"
	"github.com/Ad3bay0c/routex/observe"
	"github.com/Ad3bay0c/routex/tools"
	"github.com/Ad3bay0c/routex/tools/mcp"
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

	// Observability — both are nil-safe no-ops when disabled
	tracer  *observe.Tracer
	metrics *observe.Metrics
}

// NewRuntime creates a Runtime from a validated Config.
// Called by LoadConfig() after parsing YAML, or directly
// when using the programmatic API.
//
// NewRuntime does not start any goroutines — call Start() or
// StartAndRun() when you are ready to run.
func NewRuntime(cfg Config) (*Runtime, error) {
	logLevel := parseLogLevel(cfg.LogLevel)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: logLevel,
	})).With("runtime", cfg.Name)

	mem, err := buildMemoryStore(cfg.Memory)
	if err != nil {
		return nil, fmt.Errorf("routex: build memory store: %w", err)
	}

	adapter, err := llm.New(cfg.LLM)
	if err != nil {
		return nil, fmt.Errorf("routex: build llm adapter: %w", err)
	}

	logger.Info("runtime created",
		"provider", cfg.LLM.Provider,
		"model", cfg.LLM.Model,
		"memory", cfg.Memory.Backend,
	)

	ctx := context.Background()

	tracer := observe.NewNoopTracer()
	if cfg.Observability.Tracing {
		t, err := observe.NewTracer(ctx, cfg.Name, cfg.Observability.JaegerEndpoint)
		if err != nil {
			logger.Warn("tracing disabled — could not connect to endpoint",
				"endpoint", cfg.Observability.JaegerEndpoint,
				"error", err,
			)
		} else {
			tracer = t
			logger.Info("tracing enabled", "endpoint", cfg.Observability.JaegerEndpoint)
		}
	}

	metrics := observe.NewNoopMetrics()
	if cfg.Observability.Metrics {
		addr := cfg.Observability.MetricsAddr
		if addr == "" {
			addr = ":9090"
		}
		m, err := observe.NewMetrics(cfg.Name, addr)
		if err != nil {
			logger.Warn("metrics disabled — could not start server",
				"addr", addr,
				"error", err,
			)
		} else {
			metrics = m
			logger.Info("metrics enabled", "addr", addr+"/metrics")
		}
	}

	return &Runtime{
		cfg:      cfg,
		registry: tools.NewRegistry(),
		mem:      mem,
		adapter:  adapter,
		logger:   logger,
		task:     cfg.Task,
		tracer:   tracer,
		metrics:  metrics,
	}, nil
}

// RegisterTool makes a tool available to agents.
// Must be called before Start() or StartAndRun().
func (rt *Runtime) RegisterTool(t tools.Tool) {
	rt.registry.Register(t)
	rt.logger.Info("tool registered", "name", t.Name())
}

// AddAgent adds an agent to the runtime programmatically.
// Must be called before Start() or StartAndRun().
func (rt *Runtime) AddAgent(cfg agents.Config) {
	adapter, err := rt.resolveAgentAdapter(cfg)
	if err != nil {
		rt.logger.Warn("agent LLM config invalid, falling back to runtime default",
			"agent_id", cfg.ID, "error", err,
		)
		adapter = rt.adapter
	}
	agent := agents.New(cfg, adapter, rt.mem, rt.registry, rt.logger, rt.tracer, rt.metrics)
	rt.agentList = append(rt.agentList, agent)
	rt.logger.Info("agent added", "id", cfg.ID, "role", cfg.Role.String())
}

// SetTask overrides the task that will run when StartAndRun() is called.
func (rt *Runtime) SetTask(t Task) {
	rt.task = t
	rt.logger.Debug("task set", "input_len", len(t.Input))
}

// GetTask returns the current task configuration.
// Used by the CLI to read and modify individual task fields.
func (rt *Runtime) GetTask() Task {
	return rt.task
}

// SetLogLevel changes the runtime log level after construction.
// Accepts "debug", "info", "warn", "error". Rebuilds the logger.
func (rt *Runtime) SetLogLevel(level string) {
	rt.cfg.LogLevel = level
	logLevel := parseLogLevel(level)
	rt.logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: logLevel,
	})).With("runtime", rt.cfg.Name)
}

// AgentPlanEntry is one agent's summary in an execution plan.
type AgentPlanEntry struct {
	ID          string
	Role        string
	DependsOn   []string
	LLMProvider string // empty if inheriting runtime default
	LLMModel    string // empty if inheriting runtime default
}

// ExecutionPlan returns the wave-by-wave agent execution order
// without running anything. Used by the CLI --dry-run flag.
func (rt *Runtime) ExecutionPlan() [][]AgentPlanEntry {
	var waves [][]AgentPlanEntry

	// Group agents into waves the same way the scheduler would
	// We do a simplified version here — topological sort by depends_on
	type node struct {
		cfg      agents.Config
		inDegree int
	}

	nodes := make(map[string]*node, len(rt.cfg.Agents))
	for _, cfg := range rt.cfg.Agents {
		nodes[cfg.ID] = &node{cfg: cfg, inDegree: len(cfg.DependsOn)}
	}

	placed := make(map[string]bool)
	for len(placed) < len(nodes) {
		var wave []AgentPlanEntry
		for id, n := range nodes {
			if placed[id] {
				continue
			}
			// Check if all dependencies are placed
			ready := true
			for _, dep := range n.cfg.DependsOn {
				if !placed[dep] {
					ready = false
					break
				}
			}
			if ready {
				entry := AgentPlanEntry{
					ID:        id,
					Role:      n.cfg.Role.String(),
					DependsOn: n.cfg.DependsOn,
				}
				if n.cfg.LLM != nil {
					entry.LLMProvider = n.cfg.LLM.Provider
					entry.LLMModel = n.cfg.LLM.Model
				}
				wave = append(wave, entry)
			}
		}
		if len(wave) == 0 {
			break // cycle or error — scheduler will catch it properly
		}
		for _, e := range wave {
			placed[e.ID] = true
		}
		waves = append(waves, wave)
	}

	return waves
}

// autoRegisterTools walks the ToolConfigs from the YAML and tries to
// instantiate each one from the built-in registry.
//
// Built-in tools are only available if their sub-package has been imported
// (directly or via tools/all). If a tool is listed in agents.yaml but its
// sub-package was never imported, Resolve returns ErrToolNotBuiltin and a
// warning is logged — the tool must then be registered manually via
// RegisterTool(), or the appropriate sub-package must be imported.
//
//	import _ "github.com/Ad3bay0c/routex/tools/all"    // everything
//	import _ "github.com/Ad3bay0c/routex/tools/file"   // just file tools
//	import _ "github.com/Ad3bay0c/routex/tools/search" // just search tools
//
// MCP server entries (name == "mcp") are handled separately — they connect
// to a remote server and register all tools it exposes.
func (rt *Runtime) autoRegisterTools() error {
	for _, cfg := range rt.cfg.ToolConfigs {
		if _, ok := rt.registry.Get(cfg.Name); ok {
			rt.logger.Debug("tool already registered manually, skipping auto-discovery",
				"tool", cfg.Name,
			)
			continue
		}

		// A tool named "mcp" is a special entry that connects to an
		// external MCP server and registers all tools it exposes.
		if cfg.Name == "mcp" {
			serverURL := cfg.Extra["server_url"]
			if serverURL == "" {
				return fmt.Errorf("mcp tool entry missing required extra.server_url")
			}
			serverName := cfg.Extra["server_name"]
			if serverName == "" {
				serverName = serverURL
			}

			// Collect header_* keys from extra: — same pattern as http_request.
			headers := make(map[string]string)
			for k, v := range cfg.Extra {
				if strings.HasPrefix(k, "header_") {
					headerName := strings.TrimPrefix(k, "header_")
					headers[headerName] = v
				}
			}

			ctx := context.Background()
			_, err := mcp.RegisterServer(ctx, mcp.ServerConfig{
				ServerURL:  serverURL,
				ServerName: serverName,
				Headers:    headers,
			}, rt.registry, rt.logger)
			if err != nil {
				return fmt.Errorf("connect to MCP server %q: %w", serverName, err)
			}
			continue
		}

		tool, err := tools.Resolve(cfg.Name, cfg)
		if err != nil {
			var notBuiltin tools.ErrToolNotBuiltin
			if errors.As(err, &notBuiltin) {
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
func (rt *Runtime) Start(ctx context.Context) error {
	if rt.started {
		return fmt.Errorf("routex: runtime already started")
	}

	// Auto-register built-in tools listed in the YAML config.
	if err := rt.autoRegisterTools(); err != nil {
		return fmt.Errorf("routex: auto-register tools: %w", err)
	}

	// Build agents from config if none were added programmatically.
	if len(rt.agentList) == 0 {
		for _, cfg := range rt.cfg.Agents {
			adapter, err := rt.resolveAgentAdapter(cfg)
			if err != nil {
				return fmt.Errorf("routex: agent %q LLM config: %w", cfg.ID, err)
			}
			agent := agents.New(cfg, adapter, rt.mem, rt.registry, rt.logger, rt.tracer, rt.metrics)
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

	// Build the scheduler — passes the supervisor so the two cooperate on failures.
	rt.scheduler = scheduler.New(rt.agentList, rt.supervisor, rt.logger)

	// Launch the supervisor — starts all agent goroutines.
	rt.supervisor.Start(ctx)

	rt.started = true
	rt.logger.Info("runtime started", "agents", len(rt.agentList))

	return nil
}

// Run dispatches a Task to the agent crew and waits for all agents
// to complete. Returns the final Result.
func (rt *Runtime) Run(ctx context.Context, task Task) (Result, error) {
	if !rt.started {
		return Result{}, fmt.Errorf("routex: call Start() before Run()")
	}

	runID := uuid.New().String()
	ctx = scheduler.WithRunID(ctx, runID)

	rt.logger.Info("run starting",
		"run_id", runID,
		"input_len", len(task.Input),
	)

	startTime := time.Now()

	if task.MaxDuration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, task.MaxDuration)
		defer cancel()
	}

	// Start a root trace span covering the entire run.
	ctx, finishTrace := rt.tracer.StartRun(ctx, runID, task.Input)

	agentResults, runErr := rt.scheduler.Run(ctx, task.Input)

	// Finish the root span
	finishTrace(runErr)

	if runErr != nil {
		return Result{
			Error:    runErr,
			Duration: time.Since(startTime),
			TraceID:  runID,
		}, runErr
	}

	result := rt.assembleResult(runID, startTime, agentResults)

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

	elapsed := time.Since(startTime)

	// Record crew-level metrics
	rt.metrics.RecordRun(rt.cfg.Name, elapsed)

	rt.logger.Info("run complete",
		"run_id", runID,
		"duration", elapsed,
		"tokens", result.TokensUsed,
	)

	return result, nil
}

// StartAndRun is a convenience method that calls Start() then Run()
// using the task configured in agents.yaml (or set via SetTask()).
func (rt *Runtime) StartAndRun(ctx context.Context) (Result, error) {
	if err := rt.Start(ctx); err != nil {
		return Result{}, err
	}
	return rt.Run(ctx, rt.task)
}

// Stop gracefully shuts down the runtime.
func (rt *Runtime) Stop() {
	rt.logger.Info("runtime stopping")

	ctx := context.Background()

	// Flush and close the tracer — ensures no spans are lost
	if err := rt.tracer.Shutdown(ctx); err != nil {
		rt.logger.Warn("tracer shutdown error", "error", err)
	}

	// Stop the metrics HTTP server
	if err := rt.metrics.Shutdown(ctx); err != nil {
		rt.logger.Warn("metrics shutdown error", "error", err)
	}

	if rt.mem != nil {
		if err := rt.mem.Close(); err != nil {
			rt.logger.Warn("error closing memory store", "error", err)
		}
	}

	rt.started = false
	rt.logger.Info("runtime stopped")
}

// assembleResult builds the final Result from individual agent results.
func (rt *Runtime) assembleResult(
	runID string,
	startTime time.Time,
	agentResults map[string]agents.Result,
) Result {
	result := Result{
		AgentResults: make(map[string]AgentResult, len(agentResults)),
		TraceID:      runID,
		Duration:     time.Since(startTime),
		OutputFile:   rt.task.OutputFile,
	}

	for id, ar := range agentResults {
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

	result.Output = rt.findFinalOutput(agentResults)

	return result
}

// findFinalOutput finds the output of the terminal agent —
// the agent that no other agent depends on.
func (rt *Runtime) findFinalOutput(agentResults map[string]agents.Result) string {
	hasDependents := make(map[string]bool)
	for _, a := range rt.agentList {
		for _, dep := range a.DependsOn() {
			hasDependents[dep] = true
		}
	}

	for _, a := range rt.agentList {
		if !hasDependents[a.ID()] {
			if r, ok := agentResults[a.ID()]; ok {
				return r.Output
			}
		}
	}

	return ""
}

// resolveAgentAdapter returns the LLM adapter for a given agent.
// If the agent has its own LLM config, a new adapter is built from it.
// Otherwise the runtime's default adapter is returned.
//
// This is what enables multi-LLM crews — each agent can use a completely
// different provider and model from its peers.
func (rt *Runtime) resolveAgentAdapter(cfg agents.Config) (llm.Adapter, error) {
	if cfg.LLM == nil {
		return rt.adapter, nil
	}

	// Build a new adapter just for this agent
	adapter, err := llm.New(*cfg.LLM)
	if err != nil {
		return nil, fmt.Errorf("build agent LLM adapter: %w", err)
	}

	rt.logger.Info("agent using dedicated LLM",
		"agent_id", cfg.ID,
		"provider", cfg.LLM.Provider,
		"model", cfg.LLM.Model,
	)

	return adapter, nil
}

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
