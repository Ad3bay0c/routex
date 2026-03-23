package routex

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"

	"github.com/Ad3bay0c/routex/agents"
	"github.com/Ad3bay0c/routex/llm"
	"github.com/Ad3bay0c/routex/memory"
	"github.com/Ad3bay0c/routex/tools"
)

// Config is the fully parsed and validated configuration for a Runtime.
// It is built either by LoadConfig() from a YAML file,
// or directly in Go code using NewRuntime(Config{...}).
// Every field here maps to something in agents.yaml.
type Config struct {
	// Name is a human-readable label for this runtime instance.
	// Shows up in logs and traces so you can tell crews apart.
	Name string

	// LLM holds the settings for the language model provider.
	LLM llm.Config

	// Agents is the list of agents in this crew, in definition order.
	// The scheduler uses DependsOn to determine the actual run order.
	Agents []agents.Config

	// Memory holds the settings for the memory backend.
	Memory MemoryConfig

	// ToolConfigs holds the settings for each tool listed in agents.yaml.
	// Used by the runtime to auto-instantiate built-in tools.
	// Tools not found in the built-in registry must be registered manually.
	ToolConfigs []tools.ToolConfig

	// Task is the default task to run when StartAndRun() is called.
	// Can be overridden at runtime via SetTask() or the ROUTEX_TASK env var.
	Task Task

	// Observability controls tracing and metrics.
	Observability ObservabilityConfig

	// LogLevel controls how verbose the runtime logs are.
	// Valid values: "debug", "info", "warn", "error"
	LogLevel string
}

// MemoryConfig holds settings for the memory backend.
type MemoryConfig struct {
	// Backend selects which store implementation to use.
	// Valid values: "inmem", "redis"
	Backend string

	// TTL is the default time-to-live for stored values.
	// Zero means values never expire.
	TTL time.Duration

	// RedisURL is only used when Backend is "redis".
	// Example: "redis://localhost:6379"
	// Read from env var REDIS_URL if not set directly.
	RedisURL string
}

// ObservabilityConfig controls tracing and metrics export.
type ObservabilityConfig struct {
	// Tracing enables OpenTelemetry tracing when true.
	// Every LLM call and tool execution gets a span.
	Tracing bool

	// Metrics enables Prometheus metrics when true.
	// Exposes /metrics on the runtime's HTTP port.
	Metrics bool

	// JaegerEndpoint is where traces are sent.
	// Example: "http://localhost:14268/api/traces"
	// Defaults to the OTEL_EXPORTER_JAEGER_ENDPOINT env var.
	JaegerEndpoint string
}

// yamlFile mirrors the structure of agents.yaml exactly.
// We parse YAML into this first, then validate and convert
// it into Config. Keeping them separate means Config stays
// clean Go types while yamlFile can be messy raw strings.
type yamlFile struct {
	Runtime struct {
		Name        string `yaml:"name"`
		LLMProvider string `yaml:"llm_provider"`
		Model       string `yaml:"model"`
		APIKey      string `yaml:"api_key"`
		BaseURL     string `yaml:"base_url"`
		LogLevel    string `yaml:"log_level"`
		EnvFile     string `yaml:"env_file"`
	} `yaml:"runtime"`

	Task struct {
		Input       string `yaml:"input"`
		OutputFile  string `yaml:"output_file"`
		MaxDuration string `yaml:"max_duration"`
	} `yaml:"task"`

	Tools []struct {
		Name       string            `yaml:"name"`
		APIKey     string            `yaml:"api_key"`
		BaseDir    string            `yaml:"base_dir"`
		MaxResults int               `yaml:"max_results"`
		Extra      map[string]string `yaml:"extra"`
	} `yaml:"tools"`

	Agents []agent `yaml:"agents"`

	Memory struct {
		Backend  string `yaml:"backend"`
		TTL      string `yaml:"ttl"`
		RedisURL string `yaml:"redis_url"`
	} `yaml:"memory"`

	Observability struct {
		Tracing        bool   `yaml:"tracing"`
		Metrics        bool   `yaml:"metrics"`
		JaegerEndpoint string `yaml:"jaeger_endpoint"`
	} `yaml:"observability"`
}

type agent struct {
	ID                    string   `yaml:"id"`
	Role                  string   `yaml:"role"`
	Goal                  string   `yaml:"goal"`
	Tools                 []string `yaml:"tools"`
	DependsOn             []string `yaml:"depends_on"`
	Restart               string   `yaml:"restart"`
	MaxRetries            int      `yaml:"max_retries"`
	Timeout               string   `yaml:"timeout"`
	MaxDuplicateToolCalls int      `yaml:"max_duplicate_tool_calls"`
	MaxTotalToolCalls     int      `yaml:"max_total_tool_calls"`
}

// LoadConfig reads a YAML file from disk, parses it, validates every field,
// and returns a Runtime ready to have tools registered and be started.
//
// Errors are returned early and clearly — if your agents.yaml has a typo
// in a role name or references a tool that is not registered, you find out
// here, before any goroutine starts or any API key is used.
//
// Usage:
//
//	rt, err := routex.LoadConfig("agents.yaml")
//	if err != nil {
//	    log.Fatal(err)  // tells you exactly what is wrong and where
//	}
func LoadConfig(path string) (*Runtime, error) {
	// — read the file from disk
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("routex: cannot read config file %q: %w", path, err)
	}

	// — peek at env_file before full parsing.
	// We do a minimal parse first so we can load the .env file
	// before resolveEnvValue() runs on api_key and other env: references.
	// Without this two-pass approach, "env:ANTHROPIC_API_KEY" resolves
	// to "" because the variable has not been loaded into os.Environ yet.
	var peek struct {
		Runtime struct {
			EnvFile string `yaml:"env_file"`
		} `yaml:"runtime"`
	}
	if err := yaml.Unmarshal(data, &peek); err == nil {
		if err := loadEnvFile(peek.Runtime.EnvFile, path); err != nil {
			return nil, fmt.Errorf("routex: load env file: %w", err)
		}
	}

	// — full YAML parse now that env vars are loaded into os.Environ
	var raw yamlFile
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("routex: invalid YAML in %q: %w", path, err)
	}

	// — convert and validate into a clean Config
	cfg, err := buildConfig(raw)
	if err != nil {
		return nil, fmt.Errorf("routex: config error in %q: %w", path, err)
	}

	// — build the Runtime from the validated config
	return NewRuntime(cfg)
}

// buildConfig converts a raw yamlFile into a validated Config.
// All validation lives here — invalid values produce clear error messages.
func buildConfig(raw yamlFile) (Config, error) {
	cfg := Config{}

	// ── Runtime section ───────────────────────────────────────────
	cfg.Name = envOr(raw.Runtime.Name, "routex")
	cfg.LogLevel = envOr(raw.Runtime.LogLevel, "info")

	// ── LLM section ───────────────────────────────────────────────
	cfg.LLM.Provider = env(raw.Runtime.LLMProvider)
	if cfg.LLM.Provider == "" {
		return cfg, fmt.Errorf("runtime.llm_provider is required — valid values: anthropic, openai, ollama")
	}

	cfg.LLM.Model = env(raw.Runtime.Model)
	if cfg.LLM.Model == "" {
		return cfg, fmt.Errorf("runtime.model is required — example: claude-sonnet-4-6")
	}

	cfg.LLM.APIKey = env(raw.Runtime.APIKey)
	if cfg.LLM.APIKey == "" && cfg.LLM.Provider != "ollama" {
		return cfg, fmt.Errorf(
			"runtime.api_key is required for provider %q\n"+
				"  set it directly:   api_key: sk-ant-...\n"+
				"  or via env var:    api_key: env:ANTHROPIC_API_KEY",
			cfg.LLM.Provider,
		)
	}

	cfg.LLM.BaseURL = env(raw.Runtime.BaseURL)

	// ── Task section ──────────────────────────────────────────────
	// ROUTEX_TASK env var wins over the YAML value
	// env() handles the "env:MY_TASK_VAR" case too
	taskInput := os.Getenv("ROUTEX_TASK")
	if taskInput == "" {
		taskInput = env(raw.Task.Input)
	}
	cfg.Task.Input = taskInput
	cfg.Task.OutputFile = env(raw.Task.OutputFile)

	if raw.Task.MaxDuration != "" {
		d, err := time.ParseDuration(env(raw.Task.MaxDuration))
		if err != nil {
			return cfg, fmt.Errorf("task.max_duration %q is not a valid duration (example: 5m, 30s): %w",
				raw.Task.MaxDuration, err)
		}
		cfg.Task.MaxDuration = d
	}

	// ── Tools section ─────────────────────────────────────────────
	for _, t := range raw.Tools {
		if t.Name == "" {
			return cfg, fmt.Errorf("tools: every tool must have a name")
		}
		// Resolve env: in all tool string fields including extra values
		resolvedExtra := make(map[string]string, len(t.Extra))
		for k, v := range t.Extra {
			resolvedExtra[k] = env(v)
		}
		cfg.ToolConfigs = append(cfg.ToolConfigs, tools.ToolConfig{
			Name:       env(t.Name),
			APIKey:     env(t.APIKey),
			BaseDir:    env(t.BaseDir),
			MaxResults: t.MaxResults,
			Extra:      resolvedExtra,
		})
	}

	// ── Agents section ────────────────────────────────────────────
	if len(raw.Agents) == 0 {
		return cfg, errors.New("at least one agent is required under agents: ")
	}

	seenIDs := make(map[string]bool)

	for i, a := range raw.Agents {
		// ID must be present and unique
		id := env(a.ID)
		if id == "" {
			return cfg, fmt.Errorf("agents[%d]: id is required", i)
		}
		if seenIDs[id] {
			return cfg, fmt.Errorf("agents[%d]: duplicate agent id %q — every agent must have a unique id", i, a.ID)
		}
		seenIDs[id] = true

		// Role must be a known value
		role := agents.Role(env(a.Role))
		if !role.IsValid() {
			return cfg, fmt.Errorf(
				"agents[%d] %q: unknown role %q — valid roles: planner, writer, critic, executor, researcher",
				i, a.ID, a.Role,
			)
		}

		// Goal is required — it becomes part of the agent's system prompt
		goal := env(a.Goal)
		if goal == "" {
			return cfg, fmt.Errorf("agents[%d] %q: goal is required — describe what this agent should accomplish", i, a.ID)
		}

		// Parse timeout duration
		var timeout time.Duration
		if a.Timeout != "" {
			d, err := time.ParseDuration(env(a.Timeout))
			if err != nil {
				return cfg, fmt.Errorf("agents[%d] %q: timeout %q is not a valid duration (example: 60s): %w",
					i, a.ID, a.Timeout, err)
			}
			timeout = d
		}

		// Parse restart policy
		restart, err := agents.ParseRestartPolicy(env(a.Restart))
		if err != nil {
			return cfg, fmt.Errorf("agents[%d] %q: %w", i, a.ID, err)
		}

		cfg.Agents = append(cfg.Agents, agents.Config{
			ID:                    id,
			Role:                  role,
			Goal:                  goal,
			Tools:                 a.Tools,
			DependsOn:             a.DependsOn,
			MaxRetries:            a.MaxRetries,
			Timeout:               timeout,
			Restart:               restart,
			MaxDuplicateToolCalls: a.MaxDuplicateToolCalls,
			MaxTotalToolCalls:     a.MaxTotalToolCalls,
		})
	}

	// Validate depends_on references — every referenced ID must exist
	for _, a := range cfg.Agents {
		for _, dep := range a.DependsOn {
			if !seenIDs[dep] {
				return cfg, fmt.Errorf(
					"agent %q depends_on %q but no agent with that id exists",
					a.ID, dep,
				)
			}
		}
	}

	// ── Memory section ────────────────────────────────────────────
	cfg.Memory.Backend = envOr(raw.Memory.Backend, "inmem")
	if cfg.Memory.Backend != "inmem" && cfg.Memory.Backend != "redis" {
		return cfg, fmt.Errorf(
			"memory.backend %q is not valid — valid values: inmem, redis",
			cfg.Memory.Backend,
		)
	}

	if raw.Memory.TTL != "" {
		d, err := time.ParseDuration(env(raw.Memory.TTL))
		if err != nil {
			return cfg, fmt.Errorf("memory.ttl %q is not a valid duration (example: 1h): %w",
				raw.Memory.TTL, err)
		}
		cfg.Memory.TTL = d
	}

	// Redis URL — env() handles "env:REDIS_URL", then fall back to
	// the bare REDIS_URL env var for users who don't use the env: prefix
	cfg.Memory.RedisURL = env(raw.Memory.RedisURL)
	if cfg.Memory.Backend == "redis" && cfg.Memory.RedisURL == "" {
		cfg.Memory.RedisURL = os.Getenv("REDIS_URL")
	}
	if cfg.Memory.Backend == "redis" && cfg.Memory.RedisURL == "" {
		return cfg, fmt.Errorf(
			"memory.backend is redis but no redis_url is set\n" +
				"  set it in yaml:   redis_url: redis://localhost:6379\n" +
				"  or via env var:   REDIS_URL=redis://localhost:6379",
		)
	}

	// ── Observability section ─────────────────────────────────────
	cfg.Observability.Tracing = raw.Observability.Tracing
	cfg.Observability.Metrics = raw.Observability.Metrics
	cfg.Observability.JaegerEndpoint = envOr(
		raw.Observability.JaegerEndpoint,
		os.Getenv("OTEL_EXPORTER_JAEGER_ENDPOINT"),
	)

	return cfg, nil
}

// loadEnvFile loads a .env file into the process environment.
// Called at the very start of LoadConfig() before any env: references
// are resolved — this is what makes "env:ANTHROPIC_API_KEY" work without
// the user having to run `export` commands in their terminal.
//
// ── Development vs Production ─────────────────────────────────────
//
// DEVELOPMENT — use env_file to load a local .env file:
//
//	runtime:
//	  env_file: "."          # loads .env next to agents.yaml
//	  env_file: ".env.local" # or a named file
//
// PRODUCTION — omit env_file entirely. Never use .env files in production.
// Inject secrets through your deployment platform instead:
//   - Docker:      --env-file or -e flags, or Docker secrets
//   - Kubernetes:  Secrets mounted as env vars or a secrets manager
//   - AWS:         Parameter Store, Secrets Manager, or ECS task definitions
//   - Fly.io:      fly secrets set KEY=value
//   - Railway:     Dashboard environment variables
//   - Heroku:      heroku config:set KEY=value
//
// .env files in production are a security risk — they can be accidentally
// committed, leaked in logs, or exposed in container images.
//
// envFile behaviour:
//
//	""           — no env file loading (correct for production)
//	"."          — loads .env from the config file's directory (development)
//	".env.local" — loads a specific named file (development)
//
// Variables already set in the environment are never overwritten.
// godotenv follows this convention — platform secrets always win over the file.
func loadEnvFile(envFile, configPath string) error {
	if envFile == "" {
		// Not set — correct for production. Platform injects env vars directly.
		return nil
	}

	// Resolve the config file's directory to an absolute path.
	// This is the security boundary — the env file must stay inside it.
	configDir, err := filepath.Abs(filepath.Dir(configPath))
	if err != nil {
		return fmt.Errorf("cannot resolve config directory: %w", err)
	}

	// Resolve the requested env file path.
	var envPath string
	if envFile == "." {
		envPath = filepath.Join(configDir, ".env")
	} else {
		// filepath.Join + filepath.Clean together resolve any .. segments.
		// e.g. "../.env" becomes the parent directory's .env before we check it.
		envPath = filepath.Clean(filepath.Join(configDir, envFile))
	}

	// ── Path traversal check ──────────────────────────────────────
	// Verify the resolved path is inside the config directory.
	// filepath.Abs ensures both sides are canonical before comparing —
	// symlinks are not resolved here intentionally (we check the path
	// string, not the filesystem destination).
	//
	// Example of what this blocks:
	//   configDir: /home/user/project
	//   envFile:   "../.env"
	//   envPath:   /home/user/.env   ← outside configDir — rejected
	//
	// We require the env file to be at or below the config file's directory.
	// If you genuinely need a shared .env above the project, set the env
	// vars in your shell or use your platform's secret management instead.
	absEnvPath, err := filepath.Abs(envPath)
	if err != nil {
		return fmt.Errorf("cannot resolve env file path: %w", err)
	}

	// strings.HasPrefix on paths needs a trailing separator to avoid
	// false positives: /home/userproject would match /home/user without it.
	safeBase := configDir + string(filepath.Separator)
	if absEnvPath != configDir && !strings.HasPrefix(absEnvPath, safeBase) {
		return fmt.Errorf(
			"env_file %q resolves to %q which is outside the config directory %q\n"+
				"  env_file must point to a file inside the same directory as agents.yaml\n"+
				"  if you need a shared env file, set variables in your shell instead",
			envFile, absEnvPath, configDir,
		)
	}

	// Load the file — godotenv.Load never overwrites existing env vars
	// so platform-injected secrets always take precedence over the file.
	err = godotenv.Load(absEnvPath)
	if err != nil {
		// File not found is a soft error for "." — developer may not have
		// created their .env yet. For an explicit filename it is a hard error.
		if envFile == "." && errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf(
			"cannot load env file %q: %w\n"+
				"  in development: create the file and add your keys\n"+
				"  in production:  remove env_file from agents.yaml — use platform secrets instead",
			absEnvPath, err,
		)
	}

	return nil
}

// env resolves a YAML string value that may use the "env:VAR_NAME" syntax.
//
// Every string field in buildConfig passes through this function.
// This means any YAML string value can use env: to read from the environment:
//
//	model:      "env:MY_MODEL"           → os.Getenv("MY_MODEL")
//	api_key:    "env:ANTHROPIC_API_KEY"  → os.Getenv("ANTHROPIC_API_KEY")
//	redis_url:  "env:REDIS_URL"          → os.Getenv("REDIS_URL")
//	name:       "my-crew"                → "my-crew"  (no prefix — returned as-is)
//
// If the env var is not set, returns "".
// Use envOr() when you need a fallback for missing values.
func env(value string) string {
	if strings.HasPrefix(value, "env:") {
		varName := strings.TrimPrefix(value, "env:")
		return os.Getenv(varName)
	}
	return value
}

// envOr resolves a value with env() and returns fallback if the result is empty.
// Use this for optional fields that have a sensible default.
//
// Example:
//
//	cfg.Name = envOr(raw.Runtime.Name, "routex")
//	// → uses raw.Runtime.Name if set, "routex" if empty or env var is unset
func envOr(value, fallback string) string {
	if resolved := env(value); resolved != "" {
		return resolved
	}
	return fallback
}

// buildMemoryStore creates the right MemoryStore implementation
// based on the config. Called by NewRuntime().
func buildMemoryStore(cfg MemoryConfig) (memory.Store, error) {
	switch cfg.Backend {
	case "inmem", "":
		return memory.NewInMemStore(), nil
	case "redis":
		return memory.NewRedisStore(cfg.RedisURL, cfg.TTL)
	default:
		return nil, fmt.Errorf("unknown memory backend %q", cfg.Backend)
	}
}
