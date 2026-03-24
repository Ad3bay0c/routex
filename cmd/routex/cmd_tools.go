package main

import (
	"fmt"
	"os"
	"sort"

	"github.com/Ad3bay0c/routex/tools"

	// Import all sub-packages so their init() functions run and
	// register their built-ins before we list them.
	_ "github.com/Ad3bay0c/routex/tools/ai"
	_ "github.com/Ad3bay0c/routex/tools/comms"
	_ "github.com/Ad3bay0c/routex/tools/file"
	_ "github.com/Ad3bay0c/routex/tools/search"
	_ "github.com/Ad3bay0c/routex/tools/web"
)

const toolsUsage = `Usage:
  routex tools <subcommand> [flags]

Subcommands:
  list    List all available built-in tools

Flags (list):
  --json   Output as JSON instead of table

Examples:
  routex tools list
  routex tools list --json`

func toolsCommand(args []string) error {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, toolsUsage)
		return fatalf("subcommand required: list")
	}

	switch args[0] {
	case "list":
		return toolsListCommand(args[1:])
	case "help", "--help", "-h":
		fmt.Println(toolsUsage)
		return nil
	default:
		sub := args[0]
		fmt.Fprintf(os.Stderr, "routex tools: unknown subcommand %q\n", sub)

		knownSubs := []string{"list"}
		if suggestion, ok := suggest(sub, knownSubs); ok {
			fmt.Fprintf(os.Stderr, "\nDid you mean this?\n        routex tools %s\n", suggestion)
		}

		fmt.Fprintf(os.Stderr, "\nRun 'routex tools --help' for available subcommands.\n")
		return fmt.Errorf("unknown subcommand %q", sub)
	}
}

func toolsListCommand(args []string) error {
	var jsonOut string
	flags := map[string]*string{"json": &jsonOut}

	fmt.Printf("\nroutex tools list: %+v\n\n ", args)
	_, err := parseFlags(args, flags)
	if err != nil {
		return err
	}

	// Collect all built-in tool names and their schemas
	type toolInfo struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Parameters  int    `json:"parameters"`
	}

	builtins := tools.ListBuiltins()
	sort.Strings(builtins)

	var infos []toolInfo
	for _, name := range builtins {
		// Resolve with an empty config to get the schema — tools that
		// require an API key will fail, so we just capture what we can.
		schema, ok := tools.SchemaForBuiltin(name)
		if !ok {
			infos = append(infos, toolInfo{Name: name})
			continue
		}
		desc := schema.Description
		if len(desc) > 80 {
			desc = desc[:77] + "..."
		}
		infos = append(infos, toolInfo{
			Name:        name,
			Description: desc,
			Parameters:  len(schema.Parameters),
		})
	}

	if jsonOut == "true" {
		data, _ := marshalJSON(infos)
		fmt.Println(string(data))
		return nil
	}

	fmt.Printf("\nBuilt-in tools (%d)\n\n", len(infos))
	fmt.Printf("  %-25s  %s\n", "NAME", "DESCRIPTION")
	fmt.Printf("  %-25s  %s\n", "────────────────────────", "─────────────────────────────────────────────")
	for _, t := range infos {
		fmt.Printf("  %-25s  %s\n", t.Name, t.Description)
	}
	fmt.Println()
	fmt.Println("Configure any tool in agents.yaml under the tools: section.")
	fmt.Println("See https://github.com/Ad3bay0c/routex for configuration details.")
	fmt.Println()
	return nil
}
