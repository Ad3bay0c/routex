// Package routex is a lightweight multi-agent AI runtime for Go.
//
// Routex lets you build AI agent crews using goroutines and channels —
// the primitives Go developers already know. Define your crew in YAML
// or pure Go, connect Anthropic, OpenAI, or Ollama, and let the runtime
// handle scheduling, parallelism, retries, and observability.
//
// # Quick start
//
//	import (
//	    "github.com/Ad3bay0c/routex"
//	    _ "github.com/Ad3bay0c/routex/tools/all"
//	)
//
//	rt, err := routex.LoadConfig("agents.yaml")
//	result, err := rt.StartAndRun(ctx)
//	fmt.Println(result.Output)
//
// # Key concepts
//
// An [Agent] is a goroutine with an LLM, memory, and a set of tools.
// Agents communicate through typed channels (Inbox, Output, Notify) and
// never share memory with each other.
//
// The [Runtime] schedules agents using topological sort — agents with no
// dependencies run in parallel, dependent agents wait for their upstream
// results. A single call to [Runtime.StartAndRun] runs the entire crew.
//
// The supervisor applies Erlang-style restart policies when agents fail.
// Three policies are supported: one_for_one, one_for_all, rest_for_one.
//
// # YAML configuration
//
// The recommended way to use Routex is via a YAML config file:
//
//	runtime:
//	  name:         "research-crew"
//	  llm_provider: "anthropic"
//	  model:        "claude-haiku-4-5-20251001"
//	  api_key:      "env:ANTHROPIC_API_KEY"
//
//	task:
//	  input: "Compare the top Go web frameworks"
//
//	agents:
//	  - id:   "researcher"
//	    role: "researcher"
//	    goal: "Find and summarise information about the topic"
//	    tools: ["web_search", "wikipedia"]
//
//	  - id:      "writer"
//	    role:    "writer"
//	    goal:    "Write a clear report from the research"
//	    depends: ["researcher"]
//
//	tools:
//	  - name: "web_search"
//	  - name: "wikipedia"
//
// # Tool imports
//
// Built-in tools are in separate sub-packages. Import only what you use:
//
//	import _ "github.com/Ad3bay0c/routex/tools/all"    // all 11 tools
//	import _ "github.com/Ad3bay0c/routex/tools/search" // web_search, brave_search, wikipedia
//	import _ "github.com/Ad3bay0c/routex/tools/file"   // read_file, write_file
//	import _ "github.com/Ad3bay0c/routex/tools/web"    // http_request, read_url, scrape
//
// # LLM providers
//
// Anthropic, OpenAI, and Ollama are supported. Both Anthropic and OpenAI
// adapters use direct HTTP with no SDK dependency. Each agent can use a
// different provider — multi-LLM crews are supported out of the box.
//
// # MCP tool servers
//
// Routex connects to any MCP (Model Context Protocol) server at startup
// and registers all its tools automatically:
//
//	tools:
//	  - name: "mcp"
//	    extra:
//	      server_url:           "http://localhost:3000"
//	      server_name:          "github"
//	      header_Authorization: "env:GITHUB_TOKEN"
//
// For full documentation, examples, and the CLI reference, see:
// https://github.com/Ad3bay0c/routex
package routex
