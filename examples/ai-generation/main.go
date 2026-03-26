// ai-generation example — Group 2 tools: summarise, translate, generate_image
//
// This example shows three AI-powered tools that transform content:
//
//	summarise       — compress long text using Claude Haiku
//	translate       — translate text using DeepL API
//	generate_image  — create images using DALL-E 3
//
// The crew runs a five-agent pipeline:
//
//	researcher → summariser → translator  → writer
//	                        → image_creator ↗
//
// Notice that translator and image_creator both depend on summariser
// and run in parallel — the scheduler launches them simultaneously
// and writer only starts once both finish.
//
// Prerequisites:
//
//	cp .env.example .env
//	# Fill in ANTHROPIC_API_KEY, DEEPL_API_KEY, OPENAI_API_KEY
//
// Run:
//
//	go run .
//
// Output: ./outputs/final_report.md + ./outputs/images/cover.png
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/Ad3bay0c/routex"
)

func main() {
	ctx := context.Background()

	// ── Load config ───────────────────────────────────────────────
	// All five tools are declared in agents.yaml with their API keys.
	// Built-in tools (summarise, translate, generate_image, write_file,
	// web_search) are auto-registered — no RegisterTool() calls needed.
	cfgPath := getEnvOr("ROUTEX_CONFIG", "agents.yaml")

	rt, err := routex.LoadConfig(cfgPath)
	if err != nil {
		log.Fatalf("config error: %v\n\n"+
			"Make sure you have set in your .env file:\n"+
			"  ANTHROPIC_API_KEY  — runtime + summarise tool\n"+
			"  DEEPL_API_KEY      — translate tool\n"+
			"  OPENAI_API_KEY     — generate_image tool\n", err)
	}

	// ── Ensure output directories exist ──────────────────────────
	for _, dir := range []string{"outputs", "outputs/images"} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			log.Fatalf("create %s: %v", dir, err)
		}
	}

	// ── Warn about missing optional keys ─────────────────────────
	checkEnvOrWarn("DEEPL_API_KEY",
		"translate tool will fail — get a free key at https://www.deepl.com/pro-api")
	checkEnvOrWarn("OPENAI_API_KEY",
		"generate_image tool will fail — needs an OpenAI account")

	// ── Run ───────────────────────────────────────────────────────
	fmt.Println("Starting AI generation crew...")
	fmt.Println("Pipeline: researcher → summariser → [translator + image_creator] → writer")
	fmt.Println("Note: translator and image_creator run in parallel")
	fmt.Println()

	result, err := rt.StartAndRun(ctx)
	if err != nil {
		log.Fatalf("run failed: %v", err)
	}

	// ── Show results ──────────────────────────────────────────────
	fmt.Println("══════════════════════════════════════════")
	fmt.Println("GENERATION COMPLETE")
	fmt.Println("══════════════════════════════════════════")
	fmt.Println()

	// Show per-agent summary in pipeline order
	order := []string{"researcher", "summariser", "translator", "image_creator", "writer"}
	for _, agentID := range order {
		ar, ok := result.AgentResults[agentID]
		if !ok {
			continue
		}
		status := "✓"
		if ar.Error != nil {
			status = "✗ " + ar.Error.Error()
		}
		fmt.Printf("%-15s  tokens: %-5d  tool calls: %-2d  %s\n",
			agentID, ar.TokensUsed, len(ar.ToolCalls), status,
		)
		for _, tc := range ar.ToolCalls {
			fmt.Printf("  → %s\n", tc.ToolName)
		}
	}

	fmt.Println()
	fmt.Printf("Total tokens:  %d\n", result.TokensUsed)
	fmt.Printf("Total time:    %s\n", result.Duration.Round(0))

	// Show generated files
	fmt.Println()
	fmt.Println("Generated files:")
	for _, path := range []string{"outputs/final_report.md", "outputs/images/cover.png"} {
		if _, err := os.Stat(path); err == nil {
			fmt.Printf("  ✓ %s\n", path)
		}
	}

	rt.Stop()
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
