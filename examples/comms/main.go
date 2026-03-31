// comms-and-storage example — Group 3 tools: send_email, read_file, http_request
//
// This example shows tools that connect agents to the outside world:
//
//	read_file    — safely read local files (sandboxed to ./data)
//	http_request — call any REST API with auth and custom headers
//	write_file   — save output to disk (sandboxed to ./outputs)
//	send_email   — deliver the finished report via SendGrid or Resend
//
// Pipeline: researcher → writer → sender
//
//	researcher  uses http_request to fetch Go release data from GitHub API
//	writer      compiles it into a markdown report and saves it
//	sender      reads the report file and emails it
//
// The http_request tool in this example uses:
//   - A default Bearer token (GITHUB_TOKEN) from agents.yaml
//   - Default headers (Accept, X-GitHub-Api-Version) set in agents.yaml
//
// The send_email tool uses Resend — swap to SendGrid by changing
// provider: and api_key: in agents.yaml with no code changes.
//
// Prerequisites:
//
//	cp .env.example .env && fill in your keys
//	mkdir -p data outputs
//
// Run:
//
//	go run .
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

	cfgPath := getEnvOr("ROUTEX_CONFIG", "agents.yaml")

	rt, err := routex.LoadConfig(cfgPath)
	if err != nil {
		log.Fatalf("config error: %v\n\n"+
			"Make sure you have set in your .env:\n"+
			"  ANTHROPIC_API_KEY\n"+
			"  RESEND_API_KEY (or SENDGRID_API_KEY)\n"+
			"  GITHUB_TOKEN (optional — for higher GitHub rate limits)\n", err)
	}

	// Ensure directories exist
	for _, dir := range []string{"data", "outputs"} {
		if mkdirErr := os.MkdirAll(dir, 0755); mkdirErr != nil {
			log.Fatalf("create %s: %v", dir, mkdirErr)
		}
	}

	checkEnvOrWarn("GITHUB_TOKEN",
		"http_request will use GitHub's unauthenticated rate limit (60 req/hour)")
	checkEnvOrWarn("RESEND_API_KEY",
		"send_email will fail — set RESEND_API_KEY or switch to sendgrid in agents.yaml")

	fmt.Println("Starting comms crew...")
	fmt.Println("Pipeline: researcher → writer → sender")
	fmt.Println()

	result, err := rt.StartAndRun(ctx)
	if err != nil {
		log.Fatalf("run failed: %v", err)
	}

	fmt.Println("══════════════════════════════════════════")
	fmt.Println("COMPLETE")
	fmt.Println("══════════════════════════════════════════")
	fmt.Println()

	for _, agentID := range []string{"researcher", "writer", "sender"} {
		ar, ok := result.AgentResults[agentID]
		if !ok {
			continue
		}
		status := "✓"
		if ar.Error != nil {
			status = "✗ " + ar.Error.Error()
		}
		fmt.Printf("%-12s  tokens: %-5d  tool calls: %-2d  %s\n",
			agentID, ar.TokensUsed, len(ar.ToolCalls), status,
		)
		for _, tc := range ar.ToolCalls {
			fmt.Printf("  → %-20s\n", tc.ToolName)
		}
	}

	fmt.Printf("\nTotal tokens: %d | Time: %s\n",
		result.TokensUsed, result.Duration.Round(0))

	if _, err := os.Stat("outputs/report.md"); err == nil {
		fmt.Println("\nReport saved to: outputs/report.md")
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
