package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const initUsage = `Usage:
  routex init [dirname]

Scaffolds a new Routex project with:
  agents.yaml      — starter config with one planner + one writer agent
  .env.example     — template for required environment variables
  .gitignore       — ignores .env and binary outputs
  main.go          — minimal Go entrypoint

If dirname is omitted, files are created in the current directory.
If the directory already contains agents.yaml, init will not overwrite it.

Examples:
  routex init
  routex init my-crew
  routex init ./projects/research-bot`

func initCommand(args []string) error {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Println(initUsage)
		return nil
	}

	// Target directory
	dir := "."
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		dir = args[0]
	}

	// Create directory if it doesn't exist
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fatalf("create directory %q: %v", dir, err)
	}

	// Derive project name from directory
	absDir, _ := filepath.Abs(dir)
	projectName := filepath.Base(absDir)
	if projectName == "." || projectName == "" {
		projectName = "my-agents-crew"
	}

	files := scaffoldFiles(projectName)

	created := 0
	skipped := 0

	for name, content := range files {
		path := filepath.Join(dir, name)

		// Never overwrite existing files
		if _, err := os.Stat(path); err == nil {
			fmt.Printf("  skip     %s (already exists)\n", name)
			skipped++
			continue
		}

		// Create parent directories for nested files
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return fatalf("create directory for %s: %v", name, err)
		}

		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			return fatalf("write %s: %v", name, err)
		}

		fmt.Printf("  create   %s\n", name)
		created++
	}

	fmt.Printf("\nInitialised %q (%d created, %d skipped)\n\n", projectName, created, skipped)
	fmt.Println("Next steps:")
	fmt.Printf("  cd %s\n", dir)
	fmt.Println("  cp .env.example .env")
	fmt.Println("  # Add your ANTHROPIC_API_KEY to .env")
	fmt.Println("  go mod tidy")
	fmt.Println("  routex run agents.yaml")
	fmt.Println()

	return nil
}

// scaffoldFiles returns the content of every file to create.
// projectName is used to personalise the starter config.
func scaffoldFiles(projectName string) map[string]string {
	return map[string]string{
		"agents.yaml":  agentsYAML(projectName),
		".env.example": envExample(),
		".gitignore":   gitignore(),
		"main.go":      mainGo(projectName),
	}
}

func agentsYAML(name string) string {
	return fmt.Sprintf(`runtime:
  name:         %q
  llm_provider: "anthropic"
  model:        "claude-sonnet-4-6"
  api_key:      "env:ANTHROPIC_API_KEY"
  log_level:    "info"
  env_file:     "."   # DEVELOPMENT ONLY — remove in production

task:
  input:        "Research and summarise the latest developments in AI agents"
  output_file:  "./outputs/report.md"
  max_duration: "5m"

tools:
  - name: "web_search"

  - name:     "write_file"
    base_dir: "./outputs"

agents:

  - id:          "researcher"
    role:        "researcher"
    goal:        "Search for the 5 most recent and relevant articles or papers on the task topic. Summarise the key findings from each source."
    tools:       ["web_search"]
    restart:     "one_for_one"
    max_retries: 2
    timeout:     "90s"

  - id:          "writer"
    role:        "writer"
    goal:        "Take the research findings and write a clear, well-structured markdown report. Save it as 'report.md'."
    tools:       ["write_file"]
    depends_on:  ["researcher"]
    restart:     "one_for_one"
    max_retries: 2
    timeout:     "120s"

memory:
  backend: "inmem"
  ttl:     "1h"

observability:
  tracing: false
  metrics: false
`, name)
}

func envExample() string {
	return `# Copy this file to .env and fill in your keys.
# DEVELOPMENT ONLY — use platform secrets in production.

ANTHROPIC_API_KEY=sk-ant-your-key-here

# Uncomment if using OpenAI
# OPENAI_API_KEY=sk-your-key-here

# Uncomment if using Redis memory backend
# REDIS_URL=redis://localhost:6379
`
}

func gitignore() string {
	return `# Binaries
*.exe
*.out
*.test

# Secrets — never commit
.env
.env.local

# Agent outputs
outputs/

# Go
vendor/

# IDE
.vscode/
.idea/
.DS_Store
outputs
`
}

func mainGo(name string) string {
	return fmt.Sprintf(`// %s — Routex agent crew
//
// Run with:   routex run agents.yaml
// Or directly: go run .
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

	rt, err := routex.LoadConfig("agents.yaml")
	if err != nil {
		log.Fatalf("config: %%v", err)
	}

	if err := os.MkdirAll("outputs", 0755); err != nil {
		log.Fatalf("create outputs dir: %%v", err)
	}

	result, err := rt.StartAndRun(ctx)
	if err != nil {
		log.Fatalf("run failed: %%v", err)
	}

	fmt.Printf("Done in %%s — %%d tokens used\\n",
		result.Duration.Round(0), result.TokensUsed)

	if result.OutputFile != "" {
		fmt.Printf("Output: %%s\\n", result.OutputFile)
	}

	rt.Stop()
}
`, name)
}
