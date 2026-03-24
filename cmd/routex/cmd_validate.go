package main

import (
	"fmt"
	"os"

	"github.com/Ad3bay0c/routex"
)

const validateUsage = `Usage:
  routex validate <agents.yaml> [flags]

Validates a Routex config file without running any agents.
Checks: YAML syntax, required fields, agent graph (cycles, missing deps),
env vars referenced with env: are set, tools are recognised built-ins.

Flags:
  -e, --env-file  <path>   Load env file before validation.
      --json               Output result as JSON (useful in CI pipelines).

Exit codes:
  0   Config is valid
  1   Config has errors

Examples:
  routex validate agents.yaml
  routex validate agents.yaml --env-file .env.prod
  routex validate agents.yaml --json`

func validateCommand(args []string) error {
	var (
		envFile string
		jsonOut string
	)

	flags := map[string]*string{
		"e": &envFile, "env-file": &envFile,
		"json": &jsonOut,
	}

	positional, err := parseFlags(args, flags)
	if err != nil {
		fmt.Fprintln(os.Stderr, validateUsage)
		return err
	}
	if positional == nil {
		fmt.Println(validateUsage)
		return nil
	}
	if len(positional) < 1 {
		fmt.Fprintln(os.Stderr, validateUsage)
		return fatalf("agents.yaml path is required")
	}

	configPath := positional[0]

	var loadOpts []routex.LoadOption
	if envFile != "" {
		loadOpts = append(loadOpts, routex.WithEnvFile(envFile))
	}

	// Attempt to load — this runs the full parse + validation pipeline
	_, err = routex.LoadConfig(configPath, loadOpts...)

	if jsonOut == "true" {
		return printValidateJSON(configPath, err)
	}
	return printValidateHuman(configPath, err)
}

func printValidateHuman(configPath string, err error) error {
	if err != nil {
		fmt.Fprintf(os.Stderr, "\n✗  %s\n\n%v\n\n", configPath, err)
		return err
	}
	fmt.Printf("\n✓  %s is valid\n\n", configPath)
	return nil
}

func printValidateJSON(configPath string, err error) error {
	type result struct {
		Valid bool   `json:"valid"`
		File  string `json:"file"`
		Error string `json:"error,omitempty"`
	}

	out := result{Valid: err == nil, File: configPath}
	if err != nil {
		out.Error = err.Error()
	}

	data, _ := marshalJSON(out)
	fmt.Println(string(data))

	if err != nil {
		return err
	}
	return nil
}
