// routex — command line interface for the Routex AI agent runtime.
//
// Usage:
//
//	routex run      agents.yaml [flags]   Run an agent crew
//	routex validate agents.yaml [flags]   Validate config without running
//	routex tools    list        [flags]   List available built-in tools
//	routex init     [dirname]             Scaffold a new project
//	routex version                        Print version information
//
// Install:
//
//	go install github.com/Ad3bay0c/routex/cmd/routex@latest
package main

import (
	"os"
)

func main() {
	cli := newCLI()
	if err := cli.run(os.Args[1:]); err != nil {
		// Errors are already printed with context by each command.
		// A non-zero exit is the only signal the shell needs.
		os.Exit(1)
	}
}
