// programmatic example — using Routex as a Go package inside your own app.
//
// No YAML file needed. Everything is configured in Go code.
// Use this approach when:
//   - Task input comes from an HTTP request, database, or message queue
//   - You need to build agent configurations dynamically at runtime
//   - You are embedding Routex inside a larger Go application
//   - You want full control over every setting in code
//
// Run:
//
//	export ANTHROPIC_API_KEY=sk-ant-...
//	go run .
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/Ad3bay0c/routex"
	"github.com/Ad3bay0c/routex/agents"
	"github.com/Ad3bay0c/routex/llm"
	"github.com/Ad3bay0c/routex/tools"
)

func main() {
	ctx := context.Background()

	// ───────────────── Build the runtime in Go code ─────────────────────
	// NewRuntime() accepts a Config struct — every field that lives
	// in agents.yaml has a corresponding Go field here.
	rt, err := routex.NewRuntime(routex.Config{
		Name:     "research-crew",
		LogLevel: "info",
		LLM: llm.Config{
			Provider: "anthropic",
			Model:    "claude-sonnet-4-6",
			APIKey:   mustGetenv("ANTHROPIC_API_KEY"),
		},
		Memory: routex.MemoryConfig{
			Backend: "inmem",
			TTL:     time.Hour,
		},
	})
	if err != nil {
		log.Fatalf("create runtime: %v", err)
	}

	// ───────────────── Register tools ────────────────────────────────────
	rt.RegisterTool(tools.WebSearch())
	rt.RegisterTool(tools.ReadURL())

	// WriteFileIn sandboxes file writes to the ./outputs directory
	// — agents cannot write files outside it
	rt.RegisterTool(tools.WriteFileIn("./outputs"))

	// ───────────────── Add agents programmatically ──────────────────────
	// Each AddAgent() call mirrors one entry under agents: in YAML.
	// The order you call AddAgent() does not matter — depends_on
	// controls the actual execution order.
	rt.AddAgent(agents.Config{
		ID:         "planner",
		Role:       agents.Planner,
		Goal:       "Break the task into 3-5 clear, actionable research steps",
		Tools:      []string{"web_search"},
		MaxRetries: 3,
		Timeout:    60 * time.Second,
		Restart:    agents.OneForOne,
	})

	rt.AddAgent(agents.Config{
		ID:         "writer",
		Role:       agents.Writer,
		Goal:       "Follow the plan and write a thorough, well-structured report",
		Tools:      []string{"web_search", "read_url", "write_file"},
		DependsOn:  []string{"planner"},
		MaxRetries: 3,
		Timeout:    120 * time.Second,
		Restart:    agents.OneForOne,
	})

	rt.AddAgent(agents.Config{
		ID:         "critic",
		Role:       agents.Critic,
		Goal:       "Review the report for quality and completeness. Score it out of 10.",
		Tools:      []string{"write_file"},
		DependsOn:  []string{"writer"},
		MaxRetries: 2,
		Timeout:    60 * time.Second,
		Restart:    agents.OneForOne,
	})

	// ───────────────── Start the runtime ─────────────────────────────────
	if err := rt.Start(ctx); err != nil {
		log.Fatalf("start runtime: %v", err)
	}
	defer rt.Stop()

	// ───────────────── Run with a task from anywhere ─────────────────────
	// In a real app this would come from an HTTP request, Kafka message,
	// database row, CLI flag — wherever your app gets its input.
	// The runtime does not care where it came from.
	topic := getEnvOr("ROUTEX_TASK", "Write a report on the future of AI Agents in 2025")

	fmt.Printf("Running crew on topic: %q\n\n", topic)

	result, err := rt.Run(ctx, routex.Task{
		Input:       topic,
		OutputFile:  "outputs/report.md",
		MaxDuration: 5 * time.Minute,
	})
	if err != nil {
		log.Fatalf("run failed: %v", err)
	}

	// ── Step 6: Use the result however your app needs ─────────────
	// In a web app you might write result.Output to an HTTP response.
	// In a pipeline you might publish it to a message queue.
	// Here we just print it.
	fmt.Println(result.Output)
	fmt.Printf("\nTokens used: %d | Duration: %s | Trace: %s\n",
		result.TokensUsed,
		result.Duration.Round(time.Second),
		result.TraceID,
	)

	// ── Bonus: run multiple tasks on the same runtime ─────────────
	// Unlike the YAML example, the programmatic API lets you call
	// rt.Run() multiple times with different tasks.
	// The same agent goroutines handle each task in sequence.
	fmt.Println("\nRunning a second task on the same runtime...")

	result2, err := rt.Run(ctx, routex.Task{
		Input:       "Summarise the key differences between Go and Rust for systems programming",
		MaxDuration: 3 * time.Minute,
	})
	if err != nil {
		log.Fatalf("second run failed: %v", err)
	}

	fmt.Println(result2.Output)
}

// mustGetenv reads an environment variable and exits if it is not set.
// Used for required config like API keys — fail fast with a clear message.
func mustGetenv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("required environment variable %q is not set", key)
	}
	return v
}

func getEnvOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
