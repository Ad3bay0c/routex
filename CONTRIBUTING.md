# Contributing to Routex

Thank you for considering a contribution. Routex is a small, focused codebase — most contributions are straightforward once you understand the patterns we use consistently throughout.

This guide explains how to add each major extension point: tools, LLM providers, memory backends, and the CLI. It also covers the project conventions, testing requirements, and the review process.

---

## Table of Contents

- [Getting started](#getting-started)
- [Project conventions](#project-conventions)
- [Adding a built-in tool](#adding-a-built-in-tool)
- [Adding an LLM provider](#adding-an-llm-provider)
- [Adding a memory backend](#adding-a-memory-backend)
- [Adding a CLI command](#adding-a-cli-command)
- [Writing tests](#writing-tests)
- [Submitting a pull request](#submitting-a-pull-request)

---

## Getting started

### 1. Fork the repository

Click **Fork** on the top right of the [Routex GitHub page](https://github.com/Ad3bay0c/routex) to create a copy under your own account. This is important — you do not have write access to the main repository, so all changes must come through your fork.

### 2. Clone your fork

```bash
# Clone YOUR fork — replace <your-username> with your GitHub username
git clone https://github.com/<your-username>/routex.git
cd routex

# Add the original repo as upstream so you can sync changes later
git remote add upstream https://github.com/Ad3bay0c/routex.git
```

### 3. Create a feature branch

Never work directly on `main`. Create a branch that describes what you are building:

```bash
# Sync with latest upstream main before branching
git fetch upstream
git checkout -b feat/my-new-tool upstream/main
```

Good branch names:
- `feat/gemini-provider` — adding a new feature
- `fix/mcp-session-timeout` — fixing a bug
- `docs/mcp-server-example` — documentation only

### 4. Make your changes and run tests

```bash
# Install dependencies
go mod tidy

# Run the full test suite — no API keys needed, tests use mock servers
go test ./...

# Run with the race detector — this is required before every PR
go test -race ./...

# Run the linter
go vet ./...
```

All three must pass before you open a PR. The CI pipeline enforces this automatically, but catching it locally saves time.

### 5. Commit and push to your fork

```bash
git add .
git commit -m "feat: add Gemini LLM provider"
git push origin feat/my-new-tool
```

Good commit messages follow the format `type: short description` where type is one of `feat`, `fix`, `docs`, `test`, `refactor`, or `chore`.

### 6. Open a Pull Request

Go to your fork on GitHub. You will see a **Compare & pull request** button appear. Click it, fill in the PR description (template below), and submit against the `main` branch of `Ad3bay0c/routex`.

The CI pipeline runs automatically — it must pass before the PR can be merged.

---

## Project conventions

**Read these before writing any code.** They explain the "why" behind patterns you will see throughout the codebase.

### Errors are values, not panics

Functions return `error` as the last return value. We never `panic` in library code. Errors should include context:

```go
// bad
return fmt.Errorf("failed")

// good
return fmt.Errorf("brave_search: parse response: %w", err)
```

The prefix pattern `"package: operation: "` makes errors self-describing in logs without needing a stack trace.

### The `env:` prefix is universal

Any string field in `agents.yaml` can hold `"env:VAR_NAME"` to read from the environment. This is handled by `env()` and `envOr()` in `config.go`. When you add a new config field, always pass it through `env()` in `buildConfig()`:

```go
cfg.MyField = env(raw.Runtime.MyField)
```

### Nil-safe observability

All observe methods must be safe to call on a nil receiver. When tracing or metrics are disabled, the runtime passes `nil` (which defaults to `noopTracer{}` / `noopMetrics{}`). Never add a nil check at the call site:

```go
// bad
if t.tracer != nil {
    ctx, finish = t.tracer.StartToolCall(ctx, name, input)
}

// good — the method handles nil internally
ctx, finish := t.tracer.StartToolCall(ctx, name, input)
```

### Testability via override fields

Every struct that makes HTTP calls must have an unexported override field for the endpoint URL. When empty, the field is ignored and the real URL is used. This lets tests redirect calls to `httptest.NewServer` without any `if testing.Testing()` guards in production code:

```go
type MyTool struct {
    client   *http.Client
    apiKey   string
    url string // overrides the real URL in tests
}
```

### Compile-time interface checks

Every concrete type that implements an interface must have a compile-time check at the bottom of the file:

```go
var _ tools.Tool    = (*MyTool)(nil)
var _ llm.Adapter   = (*MyAdapter)(nil)
var _ memory.MemoryStore = (*MyStore)(nil)
```

This ensures interface changes surface as compiler errors, not runtime panics.

### Path traversal protection

Any tool that reads or writes files must protect against `../` traversal. See `write_file.go` for the canonical pattern:

```go
absBase, _ := filepath.Abs(t.baseDir)
absFull, _ := filepath.Abs(fullPath)
safeBase := absBase + string(filepath.Separator)
if absFull != absBase && !strings.HasPrefix(absFull, safeBase) {
    return nil, fmt.Errorf("path %q is outside allowed directory", params.Path)
}
```

---

## Adding a built-in tool

A built-in tool is one that the runtime auto-registers from `agents.yaml` without the user calling `rt.RegisterTool()`. The pattern has four parts.

### 1. Choose the right sub-package

Tools are organised by category:

| Sub-package | For |
|---|---|
| `tools/search/` | Web search, knowledge bases |
| `tools/web/` | HTTP requests, page fetching |
| `tools/file/` | File read/write |
| `tools/ai/` | LLM-powered tools, image generation |
| `tools/comms/` | Email, messaging, notifications |

If your tool doesn't fit any of these, propose a new sub-package in the PR description.

### 2. Implement the Tool interface

Create `tools/<category>/<toolname>.go`:

```go
package search   // match the sub-package name

import (
    "context"
    "encoding/json"
    "fmt"
    "net/http"

    "github.com/Ad3bay0c/routex/tools"
)

// MySearchTool searches MyAPI.
//
// API key: https://myapi.com/docs  (free tier: X requests/month)
//
// agents.yaml:
//
//  tools:
//    - name:    "my_search"
//      api_key: "env:MY_API_KEY"
type MySearchTool struct {
    client   *http.Client
    apiKey   string
    url string // overrides real URL in tests
}

// Input type — what the LLM sends when calling this tool
type mySearchInput struct {
    Query      string `json:"query"`
    MaxResults int    `json:"max_results,omitempty"`
}

// Output type — what the tool returns to the LLM
type mySearchOutput struct {
    Query   string   `json:"query"`
    Results []string `json:"results"`
    Total   int      `json:"total"`
}

// MySearch returns a ready-to-use MySearchTool.
// Called by the built-in registry factory and by tests.
func MySearch(apiKey string) *MySearchTool {
    return &MySearchTool{
        client: &http.Client{Timeout: 15 * time.Second},
        apiKey: apiKey,
    }
}

func (t *MySearchTool) Name() string { return "my_search" }

func (t *MySearchTool) Schema() tools.Schema {
    return tools.Schema{
        Description: "Search MyAPI for information about the given query. " +
            "Use for [describe when to use this]. " +
            "Returns up to max_results results.",
        Parameters: map[string]tools.Parameter{
            "query": {
                Type:        "string",
                Description: "The search query.",
                Required:    true,
            },
            "max_results": {
                Type:        "number",
                Description: "Number of results to return (1-10). Default: 5.",
                Required:    false,
            },
        },
    }
}

func (t *MySearchTool) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
    var params mySearchInput
    if err := json.Unmarshal(input, &params); err != nil {
        return nil, fmt.Errorf("my_search: invalid input: %w", err)
    }
    if params.Query == "" {
        return nil, fmt.Errorf("my_search: query is required")
    }

    // build URL, make request, parse response...

    return json.Marshal(mySearchOutput{
        Query:   params.Query,
        Results: results,
        Total:   len(results),
    })
}

// init registers this tool with the built-in registry.
// The runtime triggers this via a blank import in runtime.go.
func init() {
    tools.RegisterBuiltin("my_search", func(cfg tools.ToolConfig) (tools.Tool, error) {
        if cfg.APIKey == "" {
            return nil, fmt.Errorf(
                "my_search requires an api_key\n" +
                    "  add to agents.yaml:  api_key: \"env:MY_API_KEY\"\n" +
                    "  then set the env:    export MY_API_KEY=your-key",
            )
        }
        t := MySearch(cfg.APIKey)
        if cfg.MaxResults > 0 {
            t.maxResults = cfg.MaxResults
        }
        return t, nil
    })
}

// compile-time interface check
var _ tools.Tool = (*MySearchTool)(nil)
```

### 3. Add a blank import in `tools/all/all.go`

This is where all built-in sub-packages are imported. Adding your sub-package here makes it available to the CLI binary and to users who import `tools/all`:

```go
// tools/all/all.go — inside the import block
import (
    // ... existing blank imports ...
    _ "github.com/Ad3bay0c/routex/tools/mypackage"
)
```

If you added to an existing sub-package (e.g. `tools/search`), this is already done — your `init()` runs whenever that sub-package is imported.

Users who don't want to import `tools/all` can import your sub-package directly:

```go
import _ "github.com/Ad3bay0c/routex/tools/mypackage"
```

### 4. Add a blank import in `tools/builtin_test.go`

```go
import (
    // ... existing blank imports ...
    _ "github.com/Ad3bay0c/routex/tools/mypackage" // for my_tool
)
```

### 5. Write tests

Create `tools/<category>/<toolname>_test.go` (or add to the existing `<category>_test.go`):

```go
package search

import (
    "context"
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "testing"
)

func TestMySearch_ReturnsResults(t *testing.T) {
    // Set up a mock server that returns fixture data
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // Optionally verify request shape
        if r.Header.Get("Authorization") == "" {
            t.Error("missing auth header")
        }
        w.Header().Set("Content-Type", "application/json")
        json.NewEncoder(w).Encode(map[string]any{
            "results": []string{"result 1", "result 2"},
        })
    }))
    t.Cleanup(srv.Close)

    // Use the endpoint override to redirect to the mock server
    tool := &MySearchTool{
        client:   srv.Client(),
        apiKey:   "test-key",
        url: srv.URL,
    }

    result, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
        "query": "test query",
    }))
    if err != nil {
        t.Fatalf("Execute() error: %v", err)
    }

    var out mySearchOutput
    mustUnmarshal(t, result, &out)

    if out.Query != "test query" {
        t.Errorf("Query = %q, want %q", out.Query, "test query")
    }
    if out.Total != 2 {
        t.Errorf("Total = %d, want 2", out.Total)
    }
}

func TestMySearch_MissingQuery(t *testing.T) {
    tool := MySearch("key")
    _, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{}))
    if err == nil {
        t.Error("should error when query is missing")
    }
}

func TestMySearch_InvalidAPIKey(t *testing.T) {
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusUnauthorized)
    }))
    t.Cleanup(srv.Close)

    tool := &MySearchTool{client: srv.Client(), apiKey: "bad", endpoint: srv.URL}
    _, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{"query": "test"}))
    if err == nil {
        t.Error("should error for 401")
    }
}

func TestMySearch_NameAndSchema(t *testing.T) {
    tool := MySearch("key")
    if tool.Name() != "my_search" {
        t.Errorf("Name() = %q, want %q", tool.Name(), "my_search")
    }
    if tool.Schema().Description == "" {
        t.Error("Schema.Description should not be empty")
    }
}
```

### 6. Update the README

Add your tool to the built-in tools table in `README.md` with its name, required key, free tier, and a one-line description.

### 7. Update agents.yaml examples

Add a commented example of your tool in `examples/yaml-driven/agents.yaml` and in `examples/search-and-data/agents.yaml` (or whichever example is most relevant).

---

## Adding an LLM provider

New providers go in `llm/<providername>.go`.

### 1. Implement the Adapter interface

```go
// llm/myprovider.go
package llm

import (
    "context"
    "fmt"
    "time"
)

type MyProviderAdapter struct {
    model       string
    apiKey      string
    maxTokens   int
    temperature float64
    timeout     time.Duration
}

func NewMyProviderAdapter(cfg Config) (*MyProviderAdapter, error) {
    if cfg.APIKey == "" {
        return nil, fmt.Errorf("myprovider: api_key is required")
    }
    maxTokens := cfg.MaxTokens
    if maxTokens == 0 {
        maxTokens = 4096
    }
    timeout := cfg.Timeout
    if timeout == 0 {
        timeout = 120 * time.Second
    }
    return &MyProviderAdapter{
        model:       cfg.Model,
        apiKey:      cfg.APIKey,
        maxTokens:   maxTokens,
        temperature: cfg.Temperature,
        timeout:     timeout,
    }, nil
}

func (a *MyProviderAdapter) Complete(ctx context.Context, req Request) (Response, error) {
    ctx, cancel := context.WithTimeout(ctx, a.timeout)
    defer cancel()

    // 1. Convert req.History to provider message format
    // 2. Convert req.ToolSchemas to provider tool format
    // 3. Build and execute the API request
    // 4. Translate provider response to Response{Content, ToolCall, Usage, FinishReason}

    return Response{}, nil // replace with real implementation
}

func (a *MyProviderAdapter) Model() string    { return a.model }
func (a *MyProviderAdapter) Provider() string { return "myprovider" }

var _ Adapter = (*MyProviderAdapter)(nil)
```

### 2. Register in `llm.New()`

```go
// llm/adapter.go — inside New()
func New(cfg Config) (Adapter, error) {
    switch cfg.Provider {
    case "anthropic":
        return NewAnthropicAdapter(cfg)
    case "openai":
        return NewOpenAIAdapter(cfg)
    case "ollama":
        return NewOllamaAdapter(cfg)
    case "myprovider":          // add here
        return NewMyProviderAdapter(cfg)
    default:
        return nil, fmt.Errorf(
            "unknown llm provider %q — valid options are: anthropic, openai, ollama, myprovider",
            cfg.Provider,
        )
    }
}
```

### 3. Write tests

Follow the same pattern as `llm/llm_test.go` — use `httptest.NewServer` to serve fixture JSON and point the adapter's HTTP client at the mock server. Tests must cover:

- Text response → `Response.Content` populated, `Response.ToolCall` nil
- Tool call response → `Response.ToolCall` populated, `Response.Content` empty
- Auth error (401) → error returned
- `Model()` and `Provider()` return correct strings
- `NewMyProviderAdapter()` errors on missing API key

### 4. Update documentation

Add the provider to the LLM providers table in `README.md`.

---

## Adding a memory backend

New backends go in `memory/<backendname>.go`.

### 1. Implement the MemoryStore interface

```go
// memory/store.go — the interface you must satisfy
type MemoryStore interface {
    Set(ctx context.Context, key, value string, ttl time.Duration) error
    Get(ctx context.Context, key string) (string, error)
    Delete(ctx context.Context, key string) error
    Append(ctx context.Context, key string, msg Message) error
    History(ctx context.Context, key string, limit int) ([]Message, error)
    ClearHistory(ctx context.Context, key string) error
    Close() error
}
```

See `memory/inmem.go` for a complete reference implementation.

### 2. Register in `buildMemoryStore()`

```go
// config.go — inside buildMemoryStore()
func buildMemoryStore(cfg MemoryConfig) (memory.MemoryStore, error) {
    switch cfg.Backend {
    case "inmem", "":
        return memory.NewInMemStore(), nil
    case "redis":
        return memory.NewRedisStore(cfg.RedisURL, cfg.TTL)
    case "mybackend":                    // add here
        return memory.NewMyBackend(cfg.MyBackendURL, cfg.TTL)
    default:
        return nil, fmt.Errorf("unknown memory backend %q", cfg.Backend)
    }
}
```

### 3. Add config fields if needed

If your backend needs config beyond `url` and `ttl`, add fields to `MemoryConfig` in `config.go` and the corresponding YAML struct.

### 4. Write tests

Follow `memory/inmem_test.go` — test all interface methods including concurrent access with the race detector (`go test -race`).

---

## Adding a CLI command

New commands go in `cmd/routex/cmd_<name>.go`.

### 1. Write the command function

```go
// cmd/routex/cmd_mycommand.go
package main

import (
    "fmt"
    "os"
)

const myCommandUsage = `Usage:
  routex mycommand [flags]

Description of what this command does.

Flags:
  --flag   <value>   Description

Examples:
  routex mycommand
  routex mycommand --flag value`

func myCommand(args []string) error {
    var flagValue string

    flags := map[string]*string{
        "flag": &flagValue,
    }

    positional, err := parseFlags(args, flags)
    if err != nil {
        fmt.Fprintln(os.Stderr, myCommandUsage)
        return err
    }
    if positional == nil { // --help
        fmt.Println(myCommandUsage)
        return nil
    }

    // implementation...
    return nil
}
```

### 2. Register in `cli.go`

```go
// cmd/routex/cli.go — inside run()
switch cmd {
case "run":
    return runCommand(args[1:])
case "validate":
    return validateCommand(args[1:])
// ... existing cases ...
case "mycommand":              // add here
    return myCommand(args[1:])
}
```

Also add it to `knownCommands` so typo correction works:

```go
var knownCommands = []string{"run", "validate", "tools", "init", "version", "help", "mycommand"}
```

And add it to `printUsage()` so it appears in the help text.

### 3. Write tests

CLI tests use `exec.Command` to run the compiled binary:

```go
// cmd/routex/mycommand_test.go
package main

import (
    "os/exec"
    "strings"
    "testing"
)

func TestMyCommand_Basic(t *testing.T) {
    cmd := exec.Command("go", "run", ".", "mycommand")
    out, err := cmd.CombinedOutput()
    if err != nil {
        t.Fatalf("command failed: %v\noutput: %s", err, out)
    }
    if !strings.Contains(string(out), "expected output") {
        t.Errorf("unexpected output: %s", out)
    }
}
```

---

## Writing tests

### Rules

1. **No real API calls.** Every test that makes HTTP requests must use `httptest.NewServer`.
2. **No real files outside `t.TempDir()`.** Use `t.TempDir()` for any file operations — it is cleaned up automatically after the test.
3. **No real env vars.** Use `t.Setenv()` which restores the original value after the test.
4. **Race-safe.** Run `go test -race ./...` before submitting.
5. **Table-driven where appropriate.** Use `tests := []struct{...}` for multiple similar cases.

### Test naming

```
Test<Type>_<Scenario>

TestBraveSearch_ReturnsResults
TestBraveSearch_InvalidAPIKey
TestBraveSearch_MissingQuery
TestBraveSearch_NameAndSchema
```

### Common helpers

Copy these from any existing test file as needed:

```go
func mustMarshal(t *testing.T, v any) json.RawMessage {
    t.Helper()
    b, _ := json.Marshal(v)
    return b
}

func mustUnmarshal(t *testing.T, data json.RawMessage, v any) {
    t.Helper()
    json.Unmarshal(data, v)
}

func newTestServer(t *testing.T, status int, body any) *httptest.Server {
    t.Helper()
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "application/json")
        w.WriteHeader(status)
        json.NewEncoder(w).Encode(body)
    }))
    t.Cleanup(srv.Close)
    return srv
}
```

### Running specific tests

```bash
# Run all tests
go test ./...

# Run a specific package
go test ./tools/search/...

# Run a specific test
go test -run TestBraveSearch_ReturnsResults ./tools/search/

# Run with verbose output
go test -v ./tools/search/...

# Run with race detector (always do this before submitting)
go test -race ./...

# Run with coverage
go test -cover ./...
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

---

## Submitting a pull request

### Before you open the PR

Make sure your branch is up to date with upstream main to avoid merge conflicts:

```bash
git fetch upstream
git rebase upstream/main
```

Then run the full checklist locally:

```bash
go test ./...           # all tests pass
go test -race ./...     # no race conditions
go vet ./...            # no vet issues
```

### PR checklist

- [ ] Forked the repository and created a feature branch from `main`
- [ ] `go test ./...` passes
- [ ] `go test -race ./...` passes
- [ ] `go vet ./...` passes
- [ ] New code follows the project conventions above
- [ ] Tests use mock servers — no real API calls in tests
- [ ] Public API has doc comments
- [ ] `README.md` is updated if you added a tool, provider, or new CLI command
- [ ] PR description explains **what** changed and **why**

### What makes a good PR description

```
## What

Added `my_search` tool to `tools/search/` — searches MyAPI for
structured results with pagination and publication date.

## Why

Users frequently need [use case]. MyAPI provides [advantage over existing tools]
and has a generous free tier (10,000 queries/month).

## How to test

1. Get a free API key at https://myapi.com
2. Set MY_API_KEY in your .env
3. Add the tool to agents.yaml:
   tools:
     - name: "my_search"
       api_key: "env:MY_API_KEY"
4. Run: routex run examples/search-and-data/agents.yaml

## Notes

- The `endpoint` override field follows the same pattern as other tools
- Free tier keys end with `:free` — detection is automatic
```

### PR size

Keep PRs focused. A PR that adds one tool with tests and README update is ideal. A PR that adds a tool, refactors config parsing, and updates the CLI all at once is hard to review.

If you are unsure whether something is wanted before you build it, open an issue first.

---

## Questions

Open an issue with the `question` label — we will respond promptly.