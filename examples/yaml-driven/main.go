// yaml-driven example — the simplest way to use Routex.
//
// Everything is configured in agents.yaml.
// This file only does four things:
//  1. Load the config
//  2. Register tools
//  3. Run
//  4. Print the result
//
// Run:
//
//	export ANTHROPIC_API_KEY=sk-ant-...
//	go run .
//
// Override task without editing any file:
//
//	ROUTEX_TASK="What are the top Go frameworks in 2025?" go run .
//
// Use a different config file:
//
//	ROUTEX_CONFIG=my-crew.yaml go run .
package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"

	"github.com/Ad3bay0c/routex"
)

func main() {
	ctx := context.Background()

	// ─────────────── Load config from YAML ────────────────────────────
	// All agent definitions, the task, memory backend, and LLM
	// settings come from agents.yaml. Zero hardcoded values here.
	cfgPath := getEnvOr("ROUTEX_CONFIG", "agents.yaml")

	rt, err := routex.LoadConfig(cfgPath)
	if err != nil {
		// LoadConfig validates everything — if it returns an error
		// the message tells you exactly what to fix and where.
		log.Fatalf("failed to load config: %v", err)
	}

	// ─────────────── Register tools ────────────────────────────────────
	// Built-in tools listed in agents.yaml are auto-registered —
	// you do not need to call RegisterTool() for web_search,
	// read_url, or write_file. They are found automatically.
	//
	// Only call RegisterTool() for your own custom tools:
	//   rt.RegisterTool(&MyCustomTool{})
	//
	// If you want to override a built-in with your own implementation,
	// call RegisterTool() — manual registration always wins.

	// ─────────────── Run ───────────────────────────────────────────────
	// StartAndRun() starts all agent goroutines, runs the crew,
	// and blocks until every agent has finished.
	// Task input comes from agents.yaml (task.input) unless
	// ROUTEX_TASK env var is set — env var always wins.
	slog.Info("starting research crew...")

	result, err := rt.StartAndRun(ctx)
	if err != nil {
		log.Fatalf("run failed: %v", err)
	}

	// ─────────────── Print the result ──────────────────────────────────
	fmt.Println("\n" + separator("RESULT"))
	fmt.Println(result.Output)
	fmt.Println(separator(""))

	// Print a summary of what happened
	fmt.Printf("\nCompleted in %s\n", result.Duration.Round(0))
	fmt.Printf("Total tokens used: %d\n", result.TokensUsed)
	fmt.Printf("Trace ID: %s\n", result.TraceID)

	// Show per-agent breakdown
	fmt.Println("\nPer-agent breakdown:")
	for id, ar := range result.AgentResults {
		status := "ok"
		if ar.Error != nil {
			status = "failed: " + ar.Error.Error()
		}
		fmt.Printf("  %-12s  tokens: %-6d  tool calls: %-3d  status: %s\n",
			id, ar.TokensUsed, len(ar.ToolCalls), status,
		)
	}

	// Output file path — set in agents.yaml under task.output_file
	if _, err := os.Stat("report.md"); err == nil {
		fmt.Println("\nFull report saved to: report.md")
	}

	// Graceful shutdown — closes memory connections, stops goroutines
	rt.Stop()
}

func getEnvOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func separator(label string) string {
	if label == "" {
		return "────────────────────────────────────────"
	}
	return "──── " + label + " " + "────────────────────────────────────────"
}
