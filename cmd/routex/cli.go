package main

import (
	"fmt"
	"os"
	"strings"
)

// Version is set at build time via ldflags:
//
//	go build -ldflags "-X main.Version=1.0.0" ./cmd/routex
//
// Falls back to "dev" when running with `go run`.
var Version = "1.0.0"

// knownCommands is the canonical list of top-level commands.
// Used for "did you mean?" suggestions on unknown input.
var knownCommands = []string{"run", "validate", "tools", "init", "version", "help"}

// cli is the root dispatcher.
type cli struct{}

func newCLI() *cli { return &cli{} }

// run reads the first argument as a command name and delegates.
func (c *cli) run(args []string) error {
	if len(args) == 0 {
		printUsage()
		return nil
	}

	cmd := args[0]

	switch cmd {
	case "run":
		return runCommand(args[1:])
	case "validate":
		return validateCommand(args[1:])
	case "tools":
		return toolsCommand(args[1:])
	case "init":
		return initCommand(args[1:])
	case "version", "--version", "-v":
		return versionCommand(args[1:])
	case "help", "--help", "-h":
		printUsage()
		return nil
	default:
		fmt.Fprintf(os.Stderr, "routex: unknown command %q\n", cmd)

		if suggestion, ok := suggest(cmd, knownCommands); ok {
			fmt.Fprintf(os.Stderr, "\nDid you mean this?\n        routex %s\n", suggestion)
		}

		fmt.Fprintf(os.Stderr, "\nRun 'routex help' for available commands.\n")
		return fmt.Errorf("unknown command %q", cmd)
	}
}

func printUsage() {
	fmt.Print(`Routex — lightweight AI agent runtime for Go

Usage:
  routex <command> [arguments]

Commands:
  run       Run an agent crew from a YAML config file
  validate  Validate a config file without running anything
  tools     Manage and list built-in tools
  init      Scaffold a new Routex project
  version   Print version information

Examples:
  routex run agents.yaml
  routex run agents.yaml --task "Compare weather in Lagos and London"
  routex run agents.yaml --env-file .env.prod --output ./report.md
  routex validate agents.yaml
  routex tools list
  routex init my-crew

Run 'routex <command> --help' for details on a specific command.
`)
}

// fatalf prints a formatted error to stderr and returns an error.
func fatalf(format string, args ...any) error {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintln(os.Stderr, "error:", msg)
	return fmt.Errorf("%s", msg)
}

// parseFlags is a minimal flag parser that handles:
//
//	--flag value
//	--flag=value
//	-f value       (short form)
//	-f=value       (short form with equals)
func parseFlags(args []string, known map[string]*string) ([]string, error) {
	var positional []string

	for i := 0; i < len(args); i++ {
		arg := args[i]

		if !strings.HasPrefix(arg, "-") {
			positional = append(positional, arg)
			continue
		}

		name := strings.TrimLeft(arg, "-")

		// --help / -h always returns nil to signal "print help"
		if name == "help" || name == "h" {
			return nil, nil
		}

		// --flag=value form
		if idx := strings.IndexByte(name, '='); idx >= 0 {
			key := name[:idx]
			val := name[idx+1:]
			if ptr, ok := known[key]; ok {
				*ptr = val
				continue
			}
			return nil, unknownFlagError(key, known)
		}

		// Boolean flags — present means "true", no value needed
		// Detect by checking if the next token is another flag or end of args
		if ptr, ok := known[name]; ok {
			// Peek ahead: check if next arg is a flag, or we're at the end,
			// treat this as a boolean flag
			isBoolean := i+1 >= len(args) || strings.HasPrefix(args[i+1], "-")

			// Also treat as boolean if the flag is a known bool flag by convention
			// (dry-run, json have no meaningful string values)
			if isBoolean || isBoolFlag(name) {
				*ptr = "true"
				continue
			}
			// --flag <value> form
			i++
			*ptr = args[i]
			continue
		}

		return nil, unknownFlagError(name, known)
	}

	return positional, nil
}

// isBoolFlag returns true for flags that are always boolean (no value).
func isBoolFlag(name string) bool {
	switch name {
	case "dry-run", "json", "version", "help", "h", "v":
		return true
	}
	return false
}

// unknownFlagError returns an error for an unknown flag, with a
// "did you mean?" suggestion if a close match exists.
func unknownFlagError(name string, known map[string]*string) error {
	// Build the list of known flag names for suggestion
	candidates := make([]string, 0, len(known))
	seen := make(map[string]bool)
	for k := range known {
		// Skip single-char short aliases to keep suggestions readable
		if len(k) > 1 && !seen[k] {
			candidates = append(candidates, k)
			seen[k] = true
		}
	}

	msg := fmt.Sprintf("unknown flag: --%s", name)
	if suggestion, ok := suggest(name, candidates); ok {
		msg += fmt.Sprintf("\n\n        Did you mean  --%s  ?\n", suggestion)
	}

	fmt.Fprintln(os.Stderr, "error:", msg)
	return fmt.Errorf("unknown flag: --%s", name)
}
