// weather-compare — multi-LLM, four-agent pipeline
//
// Wave 1 (parallel):  lagos_weather + london_weather  — Haiku, fetch via OpenWeatherMap API
// Wave 2:             comparator                       — GPT-4o, compare (no file write)
// Wave 3:             fact_checker                     — Haiku, verify + write final report
//
// Setup:
//
//	cp .env.example .env   # fill in all four keys
//	mkdir -p outputs
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

	rt, err := routex.LoadConfig("agents3.yaml")
	if err != nil {
		log.Fatalf("config error: %v", err)
	}

	outputDir := getEnvOr("OUTPUT_DIR", "./outputs")
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		log.Fatalf("create output dir: %v", err)
	}

	fmt.Println("Starting weather crew...")
	fmt.Println()
	fmt.Println("  Wave 1 (parallel)  lagos_weather  [Haiku]")
	fmt.Println("                     london_weather [Haiku]")
	fmt.Println("  Wave 2             comparator     [GPT-4o]   — compare only, no file")
	fmt.Println("  Wave 3             fact_checker   [Haiku]    — verify + write report")
	fmt.Println()

	result, err := rt.StartAndRun(ctx)
	if err != nil {
		log.Fatalf("run failed: %v", err)
	}

	fmt.Println("══════════════════════════════════════════")
	fmt.Println("DONE")
	fmt.Println("══════════════════════════════════════════")

	for _, id := range []string{"lagos_weather", "london_weather", "comparator", "fact_checker"} {
		ar, ok := result.AgentResults[id]
		if !ok {
			continue
		}
		status := "✓"
		if ar.Error != nil {
			status = "✗  " + ar.Error.Error()
		}
		fmt.Printf("%-18s  tokens: %-5d  tool calls: %d  %s\n",
			id, ar.TokensUsed, len(ar.ToolCalls), status,
		)
	}

	fmt.Printf("\nTotal tokens : %d\n", result.TokensUsed)
	fmt.Printf("Total time   : %s\n", result.Duration.Round(0))

	outputFile := getEnvOr("WEATHER_OUTPUT_FILE", "./outputs/weather_report.md")
	if _, err := os.Stat(outputFile); err == nil {
		fmt.Printf("Report saved : %s\n", outputFile)
	}

	rt.Stop()
}

func getEnvOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
