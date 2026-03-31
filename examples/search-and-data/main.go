// search-and-data example — Group 1 tools: wikipedia, brave_search, scrape
//
// This example shows how three complementary search tools work together:
//
//	wikipedia    — instant, free background knowledge on any topic
//	brave_search — recent, structured web results with publication dates
//	scrape       — full page content including JavaScript-rendered pages
//
// The crew runs a four-agent pipeline:
//
//	background → searcher → deep_reader → writer
//
// Each agent passes its findings forward so the writer has three layers
// of research to draw from when producing the final report.
//
// Prerequisites:
//
//	export ANTHROPIC_API_KEY=sk-ant-...
//	export BRAVE_API_KEY=BSA...           # https://api.search.brave.com
//	export SCRAPINGBEE_API_KEY=...        # https://www.scrapingbee.com
//
// Run:
//
//	go run .
//
// Output: ./outputs/research_report.md
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/Ad3bay0c/routex"
)

func main() {
	ctx := context.Background()

	// ── Load config ───────────────────────────────────────────────
	// All three tools are declared in agents.yaml with their API keys.
	// The runtime auto-registers them — no manual RegisterTool() needed
	// for built-in tools. We only call RegisterTool() for custom tools.
	cfgPath := getEnvOr("ROUTEX_CONFIG", "agents.yaml")

	rt, err := routex.LoadConfig(cfgPath)
	if err != nil {
		log.Fatalf("config error: %v\n\n"+
			"Make sure you have set:\n"+
			"  ANTHROPIC_API_KEY\n"+
			"  BRAVE_API_KEY\n"+
			"  SCRAPINGBEE_API_KEY\n", err)
	}

	// ── Verify tool availability ──────────────────────────────────
	// We check for missing keys before starting so the error message
	// is clear — rather than a confusing failure mid-run.
	checkEnvOrWarn("BRAVE_API_KEY",
		"brave_search will fail — get a free key at https://api.search.brave.com")
	checkEnvOrWarn("SCRAPINGBEE_API_KEY",
		"scrape will fail — get a free key at https://www.scrapingbee.com")

	// ── Ensure output directory exists ────────────────────────────
	if mkdirErr := os.MkdirAll("outputs", 0755); mkdirErr != nil {
		log.Fatalf("create outputs dir: %v", mkdirErr)
	}

	// ── Run ───────────────────────────────────────────────────────
	fmt.Println("Starting research crew...")
	fmt.Println("Pipeline: background → searcher → deep_reader → writer")
	fmt.Println()

	result, err := rt.StartAndRun(ctx)
	if err != nil {
		log.Fatalf("run failed: %v", err)
	}

	// ── Show results ──────────────────────────────────────────────
	fmt.Println("══════════════════════════════════════════")
	fmt.Println("RESEARCH COMPLETE")
	fmt.Println("══════════════════════════════════════════")
	fmt.Println()

	// Per-agent breakdown shows exactly what each tool did
	for _, agentID := range []string{"background", "searcher", "deep_reader", "writer"} {
		ar, ok := result.AgentResults[agentID]
		if !ok {
			continue
		}

		status := "✓"
		if ar.Error != nil {
			status = "✗ " + ar.Error.Error()
		}

		fmt.Printf("%-15s tokens: %-6d  tool calls: %-3d  %s\n",
			agentID,
			ar.TokensUsed,
			len(ar.ToolCalls),
			status,
		)

		// Show which tools were called and what they searched for
		for _, tc := range ar.ToolCalls {
			fmt.Printf("  → %-20s  %s\n", tc.ToolName, summariseInput(tc.Input))
		}
	}

	fmt.Println()
	fmt.Printf("Total tokens:  %d\n", result.TokensUsed)
	fmt.Printf("Total time:    %s\n", result.Duration.Round(0))
	fmt.Printf("Trace ID:      %s\n", result.TraceID)

	if _, err := os.Stat("outputs/research_report.md"); err == nil {
		fmt.Println()
		fmt.Println("Report saved to: outputs/research_report.md")
	}

	rt.Stop()
}

// summariseInput extracts the key value from a tool call input JSON
// for display — shows the query/url/topic without printing the full JSON.
func summariseInput(inputJSON string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(inputJSON), &m); err != nil {
		return inputJSON
	}
	for _, key := range []string{"query", "url", "topic", "path"} {
		if v, ok := m[key].(string); ok {
			if len(v) > 60 {
				return v[:57] + "..."
			}
			return v
		}
	}
	return inputJSON
}

func getEnvOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func checkEnvOrWarn(key, msg string) {
	if os.Getenv(key) == "" {
		fmt.Printf("WARNING: %s not set — %s\n", key, msg)
	}
}
