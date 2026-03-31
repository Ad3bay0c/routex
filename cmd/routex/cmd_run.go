package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Ad3bay0c/routex"
)

const runUsage = `Usage:
  routex run <agents.yaml> [flags]

Flags:
  -e, --env-file   <path>      Load environment variables from this file.
                               Overrides the env_file: field in agents.yaml.
  -t, --task       <text>      Override the task input from agents.yaml.
                               Equivalent to setting ROUTEX_TASK env var.
  -o, --output     <path>      Override the output file path from agents.yaml.
  -T, --timeout    <duration>  Override max_duration. Example: "5m", "30s"
  -l, --log-level  <level>     Log verbosity: debug | info | warn | error
      --dry-run                Validate config and print execution plan, then exit.
      --json                   Print final result as JSON instead of human output.

Examples:
  routex run agents.yaml
  routex run agents.yaml -t "What is the weather in Lagos today?"
  routex run agents.yaml -e .env.prod -o ./reports/output.md
  routex run agents.yaml --timeout 10m --log-level debug
  routex run agents.yaml --dry-run`

func runCommand(args []string) error {
	return runCommandTo(os.Stdout, os.Stderr, args)
}

func runCommandTo(stdout, stderr io.Writer, args []string) error {
	var (
		envFile  string
		task     string
		output   string
		timeout  string
		logLevel string
		dryRun   string
		jsonOut  string
	)

	flags := map[string]*string{
		"e": &envFile, "env-file": &envFile,
		"t": &task, "task": &task,
		"o": &output, "output": &output,
		"T": &timeout, "timeout": &timeout,
		"l": &logLevel, "log-level": &logLevel,
		"dry-run": &dryRun,
		"json":    &jsonOut,
	}

	positional, err := parseFlags(args, flags)
	if err != nil {
		fmt.Fprintln(stderr, runUsage)
		return err
	}
	if positional == nil {
		fmt.Fprintln(stdout, runUsage)
		return nil
	}
	if len(positional) < 1 {
		fmt.Fprintln(stderr, runUsage)
		return fatalfTo(stderr, "agents.yaml path is required")
	}

	configPath := positional[0]

	var loadOpts []routex.LoadOption

	if envFile != "" {
		loadOpts = append(loadOpts, routex.WithEnvFile(envFile))
	}
	if task != "" {
		loadOpts = append(loadOpts, routex.WithTaskInput(task))
	}

	// load and validate config
	rt, err := routex.LoadConfig(configPath, loadOpts...)
	if err != nil {
		return fatalfTo(stderr, "%v", err)
	}

	// Apply flag overrides that need a live runtime
	if output != "" {
		t := rt.GetTask()
		t.OutputFile = output
		rt.SetTask(t)
	}

	if timeout != "" {
		d, err := time.ParseDuration(timeout)
		if err != nil {
			return fatalfTo(stderr, "invalid --timeout %q: must be a Go duration like 5m or 30s", timeout)
		}
		t := rt.GetTask()
		t.MaxDuration = d
		rt.SetTask(t)
	}

	if logLevel != "" {
		rt.SetLogLevel(logLevel)
	}

	// dry-run: print plan and exit
	if dryRun == "true" {
		return printExecutionPlan(stdout, rt, configPath)
	}

	// run with graceful shutdown on Ctrl+C / SIGTERM
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		select {
		case sig := <-sigCh:
			fmt.Fprintf(stderr, "\nroutex: received %s — shutting down gracefully...\n", sig)
			cancel()
		case <-ctx.Done():
		}
	}()

	if jsonOut != "true" {
		printRunHeader(stdout, configPath, rt.GetTask())
	}

	start := time.Now()
	result, runErr := rt.StartAndRun(ctx)
	elapsed := time.Since(start)

	rt.Stop()

	if jsonOut == "true" {
		return printResultJSON(stdout, stderr, result, runErr)
	}
	return printResultHuman(stdout, stderr, result, runErr, elapsed)
}

// printRunHeader prints a compact summary before execution starts.
func printRunHeader(w io.Writer, configPath string, task routex.Task) {
	fmt.Fprintf(w, "\nroutex run  %s\n", configPath)
	if task.Input != "" {
		input := task.Input
		if len(input) > 80 {
			input = input[:77] + "..."
		}
		fmt.Fprintf(w, "task        %s\n", input)
	}
	if task.OutputFile != "" {
		fmt.Fprintf(w, "output      %s\n", task.OutputFile)
	}
	if task.MaxDuration > 0 {
		fmt.Fprintf(w, "timeout     %s\n", task.MaxDuration)
	}
	fmt.Fprintln(w)
}

// printResultHuman prints a human-readable result summary.
func printResultHuman(stdout, stderr io.Writer, result routex.Result, runErr error, elapsed time.Duration) error {
	if runErr != nil && len(result.AgentResults) == 0 {
		return fatalfTo(stderr, "run failed: %v", runErr)
	}

	fmt.Fprintln(stdout, "─────────────────────────────────────────")

	for id, ar := range result.AgentResults {
		status := "✓"
		if ar.Error != nil {
			status = "✗"
		}
		fmt.Fprintf(stdout, "  %s %-20s  tokens: %-5d  calls: %d\n",
			status, id, ar.TokensUsed, len(ar.ToolCalls),
		)
		if ar.Error != nil {
			fmt.Fprintf(stdout, "    error: %v\n", ar.Error)
		}
	}

	fmt.Fprintln(stdout, "─────────────────────────────────────────")
	fmt.Fprintf(stdout, "  tokens  %d\n", result.TokensUsed)
	fmt.Fprintf(stdout, "  time    %s\n", elapsed.Round(time.Millisecond))
	if result.TraceID != "" {
		fmt.Fprintf(stdout, "  trace   %s\n", result.TraceID)
	}
	if result.OutputFile != "" {
		fmt.Fprintf(stdout, "  output  %s\n", result.OutputFile)
	}
	fmt.Fprintln(stdout)

	if runErr != nil {
		return fatalfTo(stderr, "run completed with errors: %v", runErr)
	}
	return nil
}

// printResultJSON prints the result as JSON for scripting/CI use.
func printResultJSON(stdout, stderr io.Writer, result routex.Result, runErr error) error {
	type agentJSON struct {
		ID         string `json:"id"`
		TokensUsed int    `json:"tokens_used"`
		ToolCalls  int    `json:"tool_calls"`
		Error      string `json:"error,omitempty"`
	}
	type resultJSON struct {
		Success    bool        `json:"success"`
		TokensUsed int         `json:"tokens_used"`
		TraceID    string      `json:"trace_id,omitempty"`
		OutputFile string      `json:"output_file,omitempty"`
		Agents     []agentJSON `json:"agents"`
		Error      string      `json:"error,omitempty"`
	}

	out := resultJSON{
		Success:    runErr == nil,
		TokensUsed: result.TokensUsed,
		TraceID:    result.TraceID,
		OutputFile: result.OutputFile,
	}
	if runErr != nil {
		out.Error = runErr.Error()
	}
	for id, ar := range result.AgentResults {
		a := agentJSON{
			ID:         id,
			TokensUsed: ar.TokensUsed,
			ToolCalls:  len(ar.ToolCalls),
		}
		if ar.Error != nil {
			a.Error = ar.Error.Error()
		}
		out.Agents = append(out.Agents, a)
	}

	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fatalfTo(stderr, "marshal result: %v", err)
	}
	fmt.Fprintln(stdout, string(data))

	if runErr != nil {
		return runErr
	}
	return nil
}

// printExecutionPlan prints the wave-by-wave execution order without running.
func printExecutionPlan(w io.Writer, rt *routex.Runtime, configPath string) error {
	fmt.Fprintf(w, "\nroutex dry-run  %s\n\n", configPath)

	plan := rt.ExecutionPlan()
	for i, wave := range plan {
		fmt.Fprintf(w, "  wave %d\n", i+1)
		for _, agent := range wave {
			deps := ""
			if len(agent.DependsOn) > 0 {
				deps = "  ← " + strings.Join(agent.DependsOn, ", ")
			}
			llmNote := ""
			if agent.LLMProvider != "" {
				llmNote = fmt.Sprintf("  [%s / %s]", agent.LLMProvider, agent.LLMModel)
			}
			fmt.Fprintf(w, "    %-20s%s%s\n", agent.ID, deps, llmNote)
		}
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, "Config is valid. Run without --dry-run to execute.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Note: MCP tools are not verified during dry-run — the server")
	fmt.Fprintln(w, "is contacted only when 'routex run' actually starts.")
	return nil
}
