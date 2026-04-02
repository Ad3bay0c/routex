```
  ██████╗  ██████╗ ██╗   ██╗████████╗███████╗██╗  ██╗
  ██╔══██╗██╔═══██╗██║   ██║╚══██╔══╝██╔════╝╚██╗██╔╝
  ██████╔╝██║   ██║██║   ██║   ██║   █████╗   ╚███╔╝
  ██╔══██╗██║   ██║██║   ██║   ██║   ██╔══╝   ██╔██╗
  ██║  ██║╚██████╔╝╚██████╔╝   ██║   ███████╗██╔╝ ██╗
  ╚═╝  ╚═╝ ╚═════╝  ╚═════╝    ╚═╝   ╚══════╝╚═╝  ╚═╝

  lightweight AI agent runtime for Go
```

[![Go Reference](https://pkg.go.dev/badge/github.com/Ad3bay0c/routex.svg)](https://pkg.go.dev/github.com/Ad3bay0c/routex)
[![Go Report Card](https://goreportcard.com/badge/github.com/Ad3bay0c/routex)](https://goreportcard.com/report/github.com/Ad3bay0c/routex)
[![codecov](https://codecov.io/github/Ad3bay0c/routex/graph/badge.svg?token=G9LZCMA2EC)](https://codecov.io/github/Ad3bay0c/routex)
[![GitHub stars](https://img.shields.io/github/stars/Ad3bay0c/routex?style=social)](https://github.com/Ad3bay0c/routex/stargazers)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

**A lightweight AI agent runtime for Go.**

Routex lets you build, run, and supervise multi-agent AI crews using the primitives Go developers already know — goroutines, channels, and interfaces. Define your crew in a YAML file or pure Go code, wire in any LLM provider and tools, and let the runtime handle scheduling, parallelism, retries, memory, and observability.

```bash
go install github.com/Ad3bay0c/routex/cmd/routex@latest
routex init my-crew && cd my-crew
cp .env.example .env   # add your API key
routex run agents.yaml
```

To depend on Routex from your own Go program (not the CLI):

```bash
go get github.com/Ad3bay0c/routex@latest
```

See [Using it as a library](#using-it-as-a-library) for imports and examples.

---

## Why Routex?

The AI agent ecosystem is almost entirely Python. Frameworks like LangGraph and CrewAI are powerful but they carry a Python runtime, slow cold starts, and async complexity that does not belong in production Go services.

Routex is built for Go developers who want agentic capabilities without leaving the Go ecosystem.

|                     | LangGraph  | CrewAI     | **Routex**                |
| ------------------- | ---------- | ---------- | ------------------------- |
| Language            | Python     | Python     | **Go**                    |
| Concurrency model   | asyncio    | asyncio    | **goroutines + channels** |
| Agent supervision   | none       | none       | **Erlang-style OTP tree** |
| Binary size         | heavy      | heavy      | **~10 MB single binary**  |
| Cold start          | ~2–5 s     | ~2–5 s     | **~50 ms**                |
| Multi-LLM per agent | no         | no         | **yes**                   |
| OpenTelemetry       | partial    | partial    | **first-class**           |
| Deploy target       | Python env | Python env | **any OS, Docker, K8s**   |

---

## Table of Contents

- [Concepts](#concepts)
- [Quickstart](#quickstart)
  - [Using the CLI](#using-the-cli)
  - [Using it as a library](#using-it-as-a-library)
- [YAML reference](#yaml-reference)
- [Built-in tools](#built-in-tools)
- [LLM providers](#llm-providers)
- [Multi-LLM crews](#multi-llm-crews)
- [Memory backends](#memory-backends)
- [MCP tool servers](#mcp-tool-servers)
- [Restart policies](#restart-policies)
- [Observability](#observability)
- [Writing a custom tool](#writing-a-custom-tool)
- [CLI reference](#cli-reference)
- [Environment variables](#environment-variables)
- [Repo layout](#repo-layout)
- [Roadmap](#roadmap)

---

## Concepts

Routex borrows ideas from operating systems and applies them to AI agents:

**Agent** — a long-lived goroutine with a brain (LLM), a memory scope, and a set of tools. Agents wait on an `Inbox` channel, process one task at a time, and send results back through typed channels. They never share state.

**Crew** — a collection of agents that work together on a task. Agents declare `depends_on` relationships; the scheduler turns these into a DAG and runs independent agents in parallel.

**Scheduler** — performs topological sort (Kahn's algorithm) on the dependency graph. Agents with no dependencies run immediately in parallel. Each subsequent wave starts only when the previous wave completes. Detects cycles at startup, before any agent runs.

**Supervisor** — watches agents via a dedicated `notify` channel. When an agent fails, the scheduler asks the supervisor what to do. The supervisor checks the agent's restart budget and returns a decision: retry or give up. This design means the scheduler never advances a wave past a failed agent — it blocks until the supervisor resolves the failure.

**Tool** — anything an agent can call: web search, file read/write, HTTP request, translation, image generation. Implement one interface, register once, available to any agent.

**Memory** — each agent has a namespaced key-value and message history store. In-memory by default; Redis for persistence across runs.

**Runtime** — the orchestrator. Builds the agent graph, wires in the LLM adapters, starts the supervisor, hands tasks to the scheduler, and collects results.

---

## Quickstart

### Using the CLI

The fastest path — no Go code required.

```bash
# Install
go install github.com/Ad3bay0c/routex/cmd/routex@latest

# Scaffold a new project
routex init my-research-crew
cd my-research-crew

# Set up environment
cp .env.example .env
# Edit .env — add your ANTHROPIC_API_KEY

# Edit the generated agents.yaml file

# Validate the config
routex validate agents.yaml

# See the execution plan before running
routex run agents.yaml --dry-run

# Run it
routex run agents.yaml
```

Override the task inline without editing YAML:

```bash
routex run agents.yaml --task "Compare the latest releases of Go and Rust"
```

Use a different env file (e.g. production secrets injected by your platform):

```bash
routex run agents.yaml --env-file .env.staging --output ./reports/result.md
```

---

### Using it as a library

Import Routex into any Go application. Task input can come from an HTTP request, message queue, database — anywhere.

**Add the module to your project** (this is the library dependency — unlike `go install`, which only builds the `routex` CLI):

```bash
go get github.com/Ad3bay0c/routex@latest
```

Then import the root package and any subpackages you need (see below). If you add imports first, `go mod tidy` will record the module as well.

| Goal                          | Command                                                    |
| ----------------------------- | ---------------------------------------------------------- |
| Use Routex from your Go code  | `go get github.com/Ad3bay0c/routex@version`                |
| Install the `routex` CLI only | `go install github.com/Ad3bay0c/routex/cmd/routex@version` |

**Tool imports — opt in to what you need**

Routex's built-in tools are in separate sub-packages so you only compile what you use. Import `tools/all` for everything, or pick individual packages for a leaner binary:

```go
// All 11 built-in tools (convenience — larger binary)
import _ "github.com/Ad3bay0c/routex/tools/all"

// Or import only what you need (smaller binary, fewer dependencies)
import (
    _ "github.com/Ad3bay0c/routex/tools/file"   // read_file, write_file
    _ "github.com/Ad3bay0c/routex/tools/search" // web_search, brave_search, wikipedia
    _ "github.com/Ad3bay0c/routex/tools/web"    // http_request, read_url, scrape
    // omit tools/ai   → no OpenAI/Anthropic SDK dependency
    // omit tools/comms → no SendGrid/Resend dependency
)
```

**Option 1 — YAML-driven (recommended for most use cases):**

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/Ad3bay0c/routex"
    _ "github.com/Ad3bay0c/routex/tools/all" // register all built-in tools
)

func main() {
    ctx := context.Background()

    // LoadConfig reads agents.yaml, loads .env via env_file:,
    // resolves all env: references, and validates the agent graph.
    rt, err := routex.LoadConfig("agents.yaml")
    if err != nil {
        log.Fatal(err)
    }

    result, err := rt.StartAndRun(ctx)
    if err != nil {
        log.Fatal(err)
    }

    fmt.Printf("Done in %s — %d tokens\n", result.Duration, result.TokensUsed)
    rt.Stop()
}
```

**Option 2 — override task at runtime:**

```go
rt, _ := routex.LoadConfig("agents.yaml")

// Task comes from an HTTP request, not from YAML
rt.SetTask(routex.Task{
    Input: r.FormValue("topic"),
})

// Or pass directly to LoadConfig
rt, _ := routex.LoadConfig("agents.yaml",
    routex.WithTaskInput(r.FormValue("topic")),
    routex.WithEnvFile(".env.prod"),
)

result, _ := rt.StartAndRun(ctx)
```

**Option 3 — fully programmatic (no YAML):**

```go
import (
    "github.com/Ad3bay0c/routex"
    "github.com/Ad3bay0c/routex/agents"
    "github.com/Ad3bay0c/routex/tools/search"
    "github.com/Ad3bay0c/routex/tools/file"
)

rt, _ := routex.NewRuntime(routex.Config{
    Name: "my-crew",
    LLM: routex.LLMConfig{
        Provider: "anthropic",
        Model:    "claude-sonnet-4-6",
        APIKey:   os.Getenv("ANTHROPIC_API_KEY"),
    },
})

rt.RegisterTool(search.WebSearch())
rt.RegisterTool(file.WriteFileIn("./outputs"))

rt.AddAgent(agents.AgentConfig{
    ID:    "researcher",
    Role:  agents.Researcher,
    Goal:  "Find recent news about Go 1.24",
    Tools: []string{"web_search"},
})

rt.AddAgent(agents.AgentConfig{
    ID:        "writer",
    Role:      agents.Writer,
    Goal:      "Write a summary report and save it",
    Tools:     []string{"write_file"},
    DependsOn: []string{"researcher"},
})

result, _ := rt.StartAndRun(ctx)
```

---

## YAML reference

```yaml
runtime:
  name: "my-crew" # identifies this crew in logs and traces
  llm_provider: "anthropic" # anthropic | openai | ollama
  model: "claude-sonnet-4-6" # model name for the chosen provider
  api_key: "env:ANTHROPIC_API_KEY" # env:VAR reads from environment
  log_level: "info" # debug | info | warn | error
  env_file: "." # DEVELOPMENT ONLY — load .env next to this file
  # base_url:   "env:CUSTOM_ENDPOINT"  # override LLM API endpoint

task:
  input: "Research the latest Go releases"
  output_file: "env:OUTPUT_FILE" # env: works in any string field
  max_duration: "5m" # Go duration string — 30s, 5m, 1h

tools:
  # Built-in tools — declare here, auto-registered by the runtime
  - name: "web_search" # DuckDuckGo, free, no key

  - name: "brave_search" # Higher quality, 2,000 free/month
    api_key: "env:BRAVE_API_KEY"
    max_results: 5

  - name: "wikipedia" # Free, no key needed
    extra:
      language: "en"

  - name: "http_request" # Call any REST API
    api_key: "env:MY_API_KEY" # sent as X-Api-Key header
    extra:
      bearer_token: "env:MY_TOKEN" # sent as Authorization: Bearer
      query_api_key: "env:OWM_KEY" # sent as query param (e.g. OpenWeatherMap)
      query_api_key_name: "appid" # the param name (?appid=KEY)
      param_units: "metric" # default query param on every request
      header_Accept: "application/json"

  - name: "write_file"
    base_dir: "./outputs" # agents can only write inside this dir

  - name: "read_file"
    base_dir: "./data"

  - name: "read_url" # Fetch and strip HTML from any URL

  - name: "scrape" # JS-rendered pages via ScrapingBee
    api_key: "env:SCRAPINGBEE_API_KEY"

  - name: "summarise" # LLM-powered text compression
    api_key: "env:ANTHROPIC_API_KEY"

  - name: "translate" # DeepL API, 500k chars/month free
    api_key: "env:DEEPL_API_KEY"

  - name: "generate_image" # DALL-E 3 via OpenAI
    api_key: "env:OPENAI_API_KEY"
    base_dir: "./outputs/images"

  - name: "send_email" # SendGrid or Resend
    api_key: "env:SENDGRID_API_KEY"
    extra:
      provider: "sendgrid" # sendgrid | resend
      from_email: "agent@example.com"
      from_name: "Routex Agent"

  - name: "mcp" # MCP server call
    extra:
      server_url: "http://localhost:3000"
      server_name: "github"

  - name: "mcp"
    extra:
      server_url: "http://localhost:3001"
      server_name: "postgres"

agents:
  - id: "researcher" # unique within this crew
    role: "researcher" # planner | researcher | writer | critic | executor
    goal: "Find and summarise recent Go releases"
    tools: ["web_search", "read_url"]
    restart: "one_for_one" # one_for_one | one_for_all | rest_for_one
    max_retries: 2
    timeout: "90s"
    # depends_on: []                   # list of agent IDs that must complete first
    # max_duplicate_tool_calls: 2      # redirect LLM after N identical tool calls (default: 2)
    # max_total_tool_calls: 20         # absolute tool call budget per attempt (default: 20)

    # Per-agent LLM override — uses a different model from the runtime default
    # llm:
    #   provider: "openai"
    #   model:    "gpt-4o"
    #   api_key:  "env:OPENAI_API_KEY"

  - id: "writer"
    role: "writer"
    goal: "Write a markdown report. Save it as report.md"
    tools: ["write_file"]
    depends_on: ["researcher"] # starts only after researcher finishes
    restart: "one_for_one"
    max_retries: 2
    timeout: "120s"

memory:
  backend: "inmem" # inmem | redis
  ttl: "1h"
  # redis_url: "env:REDIS_URL"         # required when backend is "redis"

observability:
  tracing: false
  metrics: false
  # jaeger_endpoint: "env:OTEL_EXPORTER_OTLP_ENDPOINT"  # e.g. http://localhost:4318
  # metrics_addr:    ":9090"           # curl localhost:9090/metrics
```

### The `env:` prefix

Any string value in the YAML can read from the environment:

```yaml
api_key: "env:ANTHROPIC_API_KEY" # reads os.Getenv("ANTHROPIC_API_KEY")
output_file: "env:OUTPUT_PATH" # works in any string field
model: "env:LLM_MODEL" # even model names
```

This means your `agents.yaml` file can be committed to git with zero secrets in it.

---

## Built-in tools

All tools are registered automatically when listed in the `tools:` section of `agents.yaml`. No `RegisterTool()` call needed for built-ins.

### Search & Data

| Tool           | Key needed            | Free tier           | Description                                 |
| -------------- | --------------------- | ------------------- | ------------------------------------------- |
| `web_search`   | none                  | unlimited           | DuckDuckGo Instant Answers                  |
| `brave_search` | `BRAVE_API_KEY`       | 2,000/month         | Structured web results with publication age |
| `wikipedia`    | none                  | unlimited           | Wikipedia article summaries, 300+ languages |
| `scrape`       | `SCRAPINGBEE_API_KEY` | 1,000 credits/month | JS-rendered page content                    |

### Web & HTTP

| Tool           | Key needed   | Description                                       |
| -------------- | ------------ | ------------------------------------------------- |
| `read_url`     | none         | Fetch and strip HTML from any URL                 |
| `http_request` | configurable | Call any REST API — GET, POST, PUT, PATCH, DELETE |

`http_request` supports four authentication patterns:

```yaml
# 1. Query string key (OpenWeatherMap, Google Maps)
extra:
  query_api_key:      "env:OWM_KEY"
  query_api_key_name: "appid"         # → ?appid=KEY on every request

# 2. Bearer token (GitHub, most REST APIs)
extra:
  bearer_token: "env:GITHUB_TOKEN"   # → Authorization: Bearer TOKEN

# 3. API key header
api_key: "env:MY_KEY"                # → X-Api-Key: KEY

# 4. Custom header
extra:
  header_X-Custom-Header: "value"    # → X-Custom-Header: value
  header_API-KEY: "value"    # → API-KEY: value
```

Default query params (sent on every request):

```yaml
extra:
  param_units: "metric" # → ?units=metric on every request
  param_lang: "en"
```

### File

| Tool         | Config     | Description                           |
| ------------ | ---------- | ------------------------------------- |
| `write_file` | `base_dir` | Write files — sandboxed to `base_dir` |
| `read_file`  | `base_dir` | Read files — sandboxed to `base_dir`  |

Path traversal (`../`) is blocked in both tools. Agents can only read or write inside the configured `base_dir`.

### AI & Generation

| Tool             | Key needed          | Description                                                 |
| ---------------- | ------------------- | ----------------------------------------------------------- |
| `summarise`      | `ANTHROPIC_API_KEY` | LLM-powered text compression (paragraph, bullets, one-line) |
| `translate`      | `DEEPL_API_KEY`     | DeepL translation, 30+ languages, 500k chars/month free     |
| `generate_image` | `OPENAI_API_KEY`    | DALL-E 3 image generation, saves to disk                    |

### Communication

| Tool         | Key needed                             | Providers        | Description                         |
| ------------ | -------------------------------------- | ---------------- | ----------------------------------- |
| `send_email` | `SENDGRID_API_KEY` or `RESEND_API_KEY` | SendGrid, Resend | Send emails with plain text or HTML |

Swap providers with one line — no code changes:

```yaml
- name: "send_email"
  api_key: "env:RESEND_API_KEY"
  extra:
    provider: "resend" # was "sendgrid"
    from_email: "agent@yourdomain.com"
```

---

## LLM providers

Set the provider at the runtime level — all agents inherit it by default:

```yaml
runtime:
  llm_provider: "anthropic" # or "openai" or "ollama"
  model: "claude-sonnet-4-6"
  api_key: "env:ANTHROPIC_API_KEY"
```

| Provider    | Models                                                              | Notes                                  |
| ----------- | ------------------------------------------------------------------- | -------------------------------------- |
| `anthropic` | `claude-sonnet-4-6`, `claude-haiku-4-5-20251001`, `claude-opus-4-6` | Default                                |
| `openai`    | `gpt-4o`, `gpt-4o-mini`, `o1`, `o3-mini`                            | Also works with OpenAI-compatible APIs |
| `ollama`    | `llama3`, `mistral`, `phi3`, any installed model                    | Local inference, no API key needed     |

Switch providers by changing two lines in YAML — zero code changes.

---

## Multi-LLM crews

Each agent can use a different LLM provider and model from the runtime default. This lets you assign the right model to each task — use a fast, cheap model for mechanical work and a powerful model only where it matters.

```yaml
runtime:
  llm_provider: "anthropic"
  model: "claude-haiku-4-5-20251001" # default — fast and cheap
  api_key: "env:ANTHROPIC_API_KEY"

agents:
  # Researcher inherits Haiku — data fetching doesn't need a powerful model
  - id: "researcher"
    role: "researcher"
    goal: "Fetch weather data from the OpenWeatherMap API"
    tools: ["http_request"]

  # Comparator uses GPT-4o — synthesis benefits from a larger model
  - id: "comparator"
    role: "writer"
    goal: "Compare the weather data and write a detailed analysis"
    tools: []
    depends_on: ["researcher"]
    llm:
      provider: "openai"
      model: "gpt-4o"
      api_key: "env:OPENAI_API_KEY"

  # Fact-checker uses Anthropic Sonnet — higher quality critique
  - id: "fact_checker"
    role: "critic"
    goal: "Verify the analysis and write the final report"
    tools: ["write_file"]
    depends_on: ["comparator"]
    llm:
      provider: "anthropic"
      model: "claude-sonnet-4-6"
      api_key: "env:ANTHROPIC_API_KEY"
```

---

## Memory backends

Agents share a namespaced memory store. Each agent's history and outputs are stored under its own key — agents cannot accidentally read each other's working memory.

```yaml
# In-memory (default) — no setup, resets between runs
memory:
  backend: "inmem"
  ttl:     "1h"

# Redis — persists across runs, shared between instances
memory:
  backend:   "redis"
  ttl:       "24h"
  redis_url: "env:REDIS_URL"
```

Start Redis locally:

```bash
docker run -p 6379:6379 redis:alpine
export REDIS_URL=redis://localhost:6379
```

---

## MCP tool servers

Routex connects to any [MCP (Model Context Protocol)](https://modelcontextprotocol.io) server at startup and registers all tools it exposes — making them available to agents exactly like built-in tools. Any MCP-compatible server works: the official GitHub MCP server, the Postgres MCP server, a custom server, or any server from the MCP registry.

```yaml
tools:
  - name: "mcp"
    extra:
      server_url: "http://localhost:3000" # required — your MCP server URL
      server_name: "github" # optional label for logs

  # Multiple servers supported — each is one entry
  - name: "mcp"
    extra:
      server_url: "http://localhost:3001"
      server_name: "postgres"
```

Tools from the server are discovered automatically via `tools/list` at startup. Agents use them by name just like built-ins:

```yaml
agents:
  - id: "researcher"
    role: "researcher"
    goal: "Search GitHub for Go MCP servers"
    tools: ["search_repos", "get_user"] # ← names returned by the MCP server
```

**Authentication** — most MCP servers require credentials. Pass them as headers using the `header_*` prefix, which supports the `env:` resolver:

```yaml
tools:
  - name: "mcp"
    extra:
      server_url: "http://localhost:3000"
      server_name: "github"
      header_Authorization: "env:GITHUB_TOKEN" # → Authorization: Bearer ghp_xxx
      header_X-Api-Key: "env:MY_API_KEY" # → X-Api-Key: abc123
```

**Name collisions** — if two servers expose a tool with the same name (e.g. both have `search`), the second is automatically prefixed with its `server_name`: `postgres.search`.

**`routex tools list`** only shows built-in tools. MCP tools are discovered at runtime — run `routex run agents.yaml --dry-run` to see all available tools after the server connection is made.

---

## Restart policies

Routex supervision is modelled after Erlang/OTP. Set per agent:

```yaml
- id: "researcher"
  restart: "one_for_one" # default
```

| Policy         | When an agent fails...                                    | Use when...                                               |
| -------------- | --------------------------------------------------------- | --------------------------------------------------------- |
| `one_for_one`  | Restart only that agent                                   | Agents are independent                                    |
| `one_for_all`  | Restart the entire crew                                   | Agents share state and a partial crew gives wrong results |
| `rest_for_one` | Restart the failed agent and all agents that depend on it | Pipeline — downstream agents need fresh upstream output   |

The restart budget is controlled at the runtime level:

```go
supervisor.New(agents, policy,
    3,             // max restarts per agent
    time.Minute,   // within this sliding window
)
```

After `maxRestarts` failures within the window, the supervisor declares the agent permanently failed and propagates the error to the caller.

---

## Observability

Enable tracing and metrics in `agents.yaml`:

```yaml
observability:
  tracing: true
  jaeger_endpoint: "http://localhost:4318" # OTLP HTTP — Jaeger v1.35+

  metrics: true
  metrics_addr: ":9090"
```

**Start Jaeger locally:**

```bash
docker run \
  -p 16686:16686 \
  -p 4318:4318 \
  jaegertracing/all-in-one

open http://localhost:16686
```

Every run produces a trace tree:

```
routex.run                        ← entire crew run
  routex.agent [researcher]       ← one agent's execution
    routex.llm.complete           ← single LLM call
    routex.tool.execute [http_request]
    routex.llm.complete           ← follow-up after tool result
  routex.agent [comparator]
    routex.llm.complete
```

**Prometheus metrics at `:9090/metrics`:**

```
routex_tokens_total{agent_id,provider}
routex_tool_calls_total{tool_name,status}
routex_tool_duration_seconds{tool_name}
routex_agent_duration_seconds{agent_id,role}
routex_run_duration_seconds{runtime_name}
routex_agent_failures_total{agent_id}
```

All observe methods are nil-safe — disabling tracing or metrics costs nothing beyond a nil check.

---

## Writing a custom tool

Any struct that implement the `Tool` interface can be registered:

```go
type Tool interface {
    Name()    string
    Schema()  tools.Schema
    Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error)
}
```

**Step 1 — implement the interface:**

```go
// tools/mytools/db_query.go
package mytools

import (
    "context"
    "database/sql"
    "encoding/json"
    "fmt"

    "github.com/Ad3bay0c/routex/tools"
)

type DBQueryTool struct {
    db *sql.DB
}

func (t *DBQueryTool) Name() string { return "db_query" }

func (t *DBQueryTool) Schema() tools.Schema {
    return tools.Schema{
        Description: "Run a read-only SQL query against the application database. " +
            "Returns results as a JSON array of objects.",
        Parameters: map[string]tools.Parameter{
            "query": {
                Type:        "string",
                Description: "A read-only SQL SELECT query. No INSERT, UPDATE, or DELETE.",
                Required:    true,
            },
        },
    }
}

type dbQueryInput struct {
    Query string `json:"query"`
}

func (t *DBQueryTool) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
    var params dbQueryInput
    if err := json.Unmarshal(input, &params); err != nil {
        return nil, fmt.Errorf("db_query: invalid input: %w", err)
    }
    if params.Query == "" {
        return nil, fmt.Errorf("db_query: query is required")
    }

    rows, err := t.db.QueryContext(ctx, params.Query)
    if err != nil {
        return nil, fmt.Errorf("db_query: %w", err)
    }
    defer rows.Close()

    // Convert rows to []map[string]any and marshal to JSON
    results, err := rowsToJSON(rows)
    if err != nil {
        return nil, fmt.Errorf("db_query: %w", err)
    }

    return json.Marshal(results)
}

// compile-time check
var _ tools.Tool = (*DBQueryTool)(nil)
```

**Step 2 — register it:**

```go
rt, _ := routex.LoadConfig("agents.yaml")
rt.RegisterTool(&mytools.DBQueryTool{db: db})
result, _ := rt.StartAndRun(ctx)
```

**Step 3 — declare it in agents.yaml:**

```yaml
tools:
  - name: "db_query" # no api_key or extra needed for custom tools

agents:
  - id: "analyst"
    role: "researcher"
    goal: "Query the orders table for last week's top products"
    tools: ["db_query"]
```

Custom tools are prioritised over built-ins — if you register a tool named `web_search`, it replaces the built-in.

---

## CLI reference

```
routex <command> [flags]

Commands:
  run       Run an agent crew
  validate  Validate config without running
  tools     Manage built-in tools
  init      Scaffold a new project
  version   Print version info
```

### `routex run`

```
routex run <agents.yaml> [flags]

Flags:
  -e, --env-file   <path>      Load .env file (overrides env_file: in YAML)
  -t, --task       <text>      Override task input
  -o, --output     <path>      Override output file path
  -T, --timeout    <duration>  Override max_duration (e.g. 10m, 30s)
  -l, --log-level  <level>     debug | info | warn | error
      --dry-run                Print execution plan and exit
      --json                   Output result as JSON

Examples:
  routex run agents.yaml
  routex run agents.yaml -t "Latest Go security advisories"
  routex run agents.yaml -e .env.staging -o ./reports/$(date +%Y%m%d).md
  routex run agents.yaml --dry-run
  routex run agents.yaml --json | jq '.agents[] | select(.error != null)'
```

The `--dry-run` flag validates the full config and prints the execution plan:

```
routex dry-run  agents.yaml

  wave 1
    lagos_weather                    ← no deps, runs first (in parallel)
    london_weather                   ← no deps, runs first (in parallel)
  wave 2
    comparator       ← lagos_weather, london_weather  [openai / gpt-4o]
  wave 3
    fact_checker     ← comparator

Config is valid. Run without --dry-run to execute.
```

### `routex validate`

```
routex validate <agents.yaml> [flags]

Flags:
  -e, --env-file  <path>   Load env file before validation
      --json               Output result as JSON (exit code 0=valid, 1=invalid)

# CI usage
routex validate agents.yaml --json | jq .
```

### `routex tools list`

```
routex tools list [flags]

Flags:
  --json   Machine-readable output

Example output:
  Built-in tools (11)

  NAME                       DESCRIPTION
  ─────────────────────────  ─────────────────────────────────────────────
  brave_search               Search the web using Brave Search for accurat...
  generate_image             Generate an image from a text description usin...
  http_request               Make an HTTP request to any REST API endpoint...
  read_file                  Read the content of a local file...
  read_url                   Fetch and strip HTML from any URL...
  scrape                     Fetch JS-rendered page content via ScrapingBee...
  send_email                 Send an email via SendGrid or Resend...
  summarise                  Compress long text using Claude Haiku...
  translate                  Translate text using DeepL...
  web_search                 Search the web via DuckDuckGo (free, no key)...
  wikipedia                  Fetch a Wikipedia article summary...
  write_file                 Write content to a file safely...
```

### `routex init`

```
routex init [dirname]

Scaffolds:
  agents.yaml     — starter config (two agents, web_search + write_file)
  .env.example    — template for required keys
  .gitignore      — ignores .env and output files
  main.go         — minimal Go entrypoint

Example:
  routex init weather-crew
  cd weather-crew
  cp .env.example .env && vim .env
  go mod init github.com/yourname/weather-crew
  go mod tidy
  routex run agents.yaml
```

### `routex version`

```
routex version [--json]

Output:
  routex 1.0.0
  go     go1.22.0
  os     linux/amd64
```

**Typo correction** — Routex suggests the closest command when you make a mistake:

```
$ routex runn agents.yaml
routex: unknown command "runn"

Did you mean this?
        routex run

$ routex run agents.yaml --timout 5m
error: unknown flag: --timout

        Did you mean  --timeout  ?
```

---

## Environment variables

| Variable                      | Used by                         | Description                                             |
| ----------------------------- | ------------------------------- | ------------------------------------------------------- |
| `ANTHROPIC_API_KEY`           | runtime, summarise              | Anthropic API key                                       |
| `OPENAI_API_KEY`              | openai provider, generate_image | OpenAI API key                                          |
| `BRAVE_API_KEY`               | brave_search                    | Brave Search API key                                    |
| `SCRAPINGBEE_API_KEY`         | scrape                          | ScrapingBee API key                                     |
| `DEEPL_API_KEY`               | translate                       | DeepL API key (append `:fx` for free tier)              |
| `SENDGRID_API_KEY`            | send_email                      | SendGrid API key                                        |
| `RESEND_API_KEY`              | send_email                      | Resend API key                                          |
| `OPENWEATHER_API_KEY`         | http_request                    | OpenWeatherMap key (passed via query param)             |
| `REDIS_URL`                   | memory                          | Redis connection URL                                    |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | observe                         | OTLP trace endpoint                                     |
| `ROUTEX_METRICS_ADDR`         | observe                         | Prometheus metrics address (default `:9090`)            |
| `ROUTEX_TASK`                 | config                          | Overrides `task.input` in YAML                          |
| `ROUTEX_ENV_FILE`             | config                          | Overrides `env_file:` in YAML (set by CLI `--env-file`) |

**Development vs Production:**

Use `env_file: "."` in `agents.yaml` to load a `.env` file during development:

```yaml
runtime:
  env_file: "." # DEVELOPMENT ONLY — remove in production
```

In production, inject secrets through your platform instead:

```bash
# Docker
docker run -e ANTHROPIC_API_KEY=sk-ant-... myimage

# Kubernetes
kubectl create secret generic routex-secrets \
  --from-literal=ANTHROPIC_API_KEY=sk-ant-...
```

---

## Repo layout

```
routex/
├── runtime.go                  # Runtime — Start, Run, Stop, ExecutionPlan
├── config.go                   # LoadConfig, YAML parsing, env: resolution
├── task.go                     # Task and Result public types
│
├── agents/
│   ├── agent.go                # Agent goroutine — think loop, tool dedup, retry
│   ├── agent_config.go         # AgentConfig, RestartPolicy, per-agent LLM
│   ├── roles.go                # Planner, Researcher, Writer, Critic, Executor
│   ├── memory.go               # Agent memory key helpers
│   └── observe.go              # AgentTracer and AgentMetrics interfaces
│
├── llm/
│   ├── adapter.go              # Adapter interface, Request, Response, Config
│   ├── anthropic.go            # Anthropic provider (claude-*)
│   ├── openai.go               # OpenAI provider + compatible APIs
│   └── ollama.go               # Local Ollama provider
│
├── memory/
│   ├── store.go                # MemoryStore interface
│   ├── inmem.go                # In-memory backend (default)
│   └── redis.go                # Redis backend
│
├── tools/
│   ├── tool.go                 # Tool interface, Registry, Schema, Parameter
│   ├── builtin.go              # RegisterBuiltin, Resolve, ListBuiltins
│   ├── search/
│   │   ├── web_search.go       # DuckDuckGo (free)
│   │   ├── brave_search.go     # Brave Search API
│   │   └── wikipedia.go        # Wikipedia REST API
│   ├── web/
│   │   ├── read_url.go         # HTML fetcher + stripper
│   │   ├── scrape.go           # JS-rendered pages via ScrapingBee
│   │   └── http_request.go     # Generic HTTP client
│   ├── file/
│   │   ├── write_file.go       # Sandboxed file writer
│   │   └── read_file.go        # Sandboxed file reader
│   ├── ai/
│   │   ├── summarise.go        # LLM text compression
│   │   ├── translate.go        # DeepL translation
│   │   └── generate_image.go   # DALL-E 3 image generation
│   └── comms/
│       └── send_email.go       # SendGrid + Resend (two providers, one interface)
│
├── observe/
│   ├── tracer.go               # OpenTelemetry spans — run, agent, LLM, tool
│   └── metrics.go              # Prometheus counters and histograms
│
├── internal/
│   ├── scheduler/
│   │   └── scheduler.go        # Kahn's sort, wave execution, failure cooperation
│   └── supervisor/
│       └── supervisor.go       # Erlang-style restart, FailureReport/Decision protocol
│
├── cmd/routex/
│   ├── main.go                 # CLI entrypoint
│   ├── cli.go                  # Command dispatcher, flag parser, did-you-mean
│   ├── cmd_run.go              # routex run
│   ├── cmd_validate.go         # routex validate
│   ├── cmd_tools.go            # routex tools list
│   ├── cmd_init.go             # routex init
│   ├── cmd_version.go          # routex version
│   └── suggest.go              # Levenshtein distance for typo correction
│
└── examples/
    ├── yaml-driven/            # Minimal YAML-based example
    ├── programmatic/           # Pure Go API example
    ├── search-and-data/        # Group 1 tools — brave_search, wikipedia, scrape
    ├── ai-generation/          # Group 2 tools — summarise, translate, generate_image
    ├── comms-and-storage/      # Group 3 tools — send_email, read_file, http_request
    └── weather-compare/        # Multi-LLM crew — parallel agents, fact-checker
```

---

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for how to add tools, LLM providers, memory backends, and more.

---

## License

MIT — see [LICENSE](LICENSE).
