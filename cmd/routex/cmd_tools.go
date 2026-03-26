package main

import (
	"fmt"
	"io"
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
	return toolsCommandTo(os.Stdout, args)
}

func toolsCommandTo(out io.Writer, args []string) error {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, toolsUsage)
		return fatalf("subcommand required: list")
	}

	switch args[0] {
	case "list":
		return toolsListCommandTo(out, args[1:])
	case "help", "--help", "-h":
		fmt.Fprintln(out, toolsUsage)
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

func toolsListCommandTo(out io.Writer, args []string) error {
	var jsonOut string
	flags := map[string]*string{"json": &jsonOut}

	_, err := parseFlags(args, flags)
	if err != nil {
		return err
	}

	type toolInfo struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Parameters  int    `json:"parameters"`
	}

	builtins := tools.ListBuiltins()
	sort.Strings(builtins)

	var infos []toolInfo
	for _, name := range builtins {
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
		fmt.Fprintln(out, string(data))
		return nil
	}

	fmt.Fprintf(out, "\nBuilt-in tools (%d)\n\n", len(infos))
	fmt.Fprintf(out, "  %-25s  %s\n", "NAME", "DESCRIPTION")
	fmt.Fprintf(out, "  %-25s  %s\n",
		"────────────────────────",
		"─────────────────────────────────────────────",
	)
	for _, t := range infos {
		fmt.Fprintf(out, "  %-25s  %s\n", t.Name, t.Description)
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Configure any tool in agents.yaml under the tools: section.")
	fmt.Fprintln(out, "See https://github.com/Ad3bay0c/routex for configuration details.")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "MCP tools are discovered at runtime by connecting to the MCP server.")
	fmt.Fprintln(out, "They do not appear here — run 'routex run agents.yaml --dry-run' to")
	fmt.Fprintln(out, "see which tools are available after the server connection is made.")
	fmt.Fprintln(out)
	return nil
}
