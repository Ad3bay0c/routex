package tools

import (
	"fmt"
	"os"
	"sync"
)

// ToolFactory is a function that creates a Tool instance.
// Built-in tools register a factory under their name.
// The factory receives a Config so tools can read API keys
// and settings without hardcoding them.
type ToolFactory func(cfg ToolConfig) (Tool, error)

// ToolConfig holds configuration passed to a tool factory
// when the runtime auto-instantiates a built-in tool.
// Values come from the tools: section of agents.yaml.
type ToolConfig struct {
	// Name is the tool's registered name — e.g. "web_search"
	Name string

	// APIKey is an optional API key for tools that need one.
	// Read from the env var specified in agents.yaml, or directly set.
	// Example: api_key: "env:BRAVE_API_KEY"
	APIKey string

	// BaseDir is used by file tools to restrict where they can write.
	// Example: base_dir: "./outputs"
	BaseDir string

	// MaxResults controls how many results search tools return.
	MaxResults int

	// Extra holds any additional tool-specific settings from the YAML
	// that do not fit the standard fields above.
	Extra map[string]string
}

// builtinRegistry is the global map of built-in tool factories.
// Protected by a mutex because tools can theoretically be registered
// from init() functions in multiple packages simultaneously.
var (
	builtinMu       sync.RWMutex
	builtinRegistry = map[string]ToolFactory{}
)

// RegisterBuiltin registers a factory function for a built-in tool name.
// Called once per tool — typically in an init() function or at package level.
//
// This is how the CLI and auto-discovery work:
// when agents.yaml lists "web_search", the runtime calls
// builtinRegistry["web_search"](cfg) to get a ready Tool.
//
// Usage (inside tools/web_search.go):
//
//	func init() {
//	    RegisterBuiltin("web_search", func(cfg ToolConfig) (Tool, error) {
//	        return WebSearch(), nil
//	    })
//	}
func RegisterBuiltin(name string, factory ToolFactory) {
	builtinMu.Lock()
	defer builtinMu.Unlock()
	builtinRegistry[name] = factory
}

// Resolve tries to instantiate a built-in tool by name.
// Returns the Tool if found and successfully created.
// Returns ErrToolNotBuiltin if the name is not a built-in —
// the caller should then look for a manually registered tool.
func Resolve(name string, cfg ToolConfig) (Tool, error) {
	builtinMu.RLock()
	factory, ok := builtinRegistry[name]
	builtinMu.RUnlock()

	if !ok {
		return nil, ErrToolNotBuiltin{Name: name}
	}

	tool, err := factory(cfg)
	if err != nil {
		return nil, fmt.Errorf("tool %q: failed to initialise: %w", name, err)
	}

	return tool, nil
}

// ListBuiltins returns the names of all registered built-in tools.
// Used by the CLI's `routex tools list` command.
func ListBuiltins() []string {
	builtinMu.RLock()
	defer builtinMu.RUnlock()

	names := make([]string, 0, len(builtinRegistry))
	for name := range builtinRegistry {
		names = append(names, name)
	}
	return names
}

// ErrToolNotBuiltin is returned by Resolve when the tool name
// is not found in the built-in registry.
// The runtime catches this and checks the manual registry next —
// so custom tools registered via rt.RegisterTool() still work.
type ErrToolNotBuiltin struct {
	Name string
}

func (e ErrToolNotBuiltin) Error() string {
	return fmt.Sprintf(
		"tool %q is not a built-in — register it manually with rt.RegisterTool()",
		e.Name,
	)
}

// resolveEnvValue handles the "env:VAR_NAME" syntax for tool configs.
// If value starts with "env:", reads the named environment variable.
// Same pattern as config.go — consistent across the whole project.
func resolveEnvValue(value string) string {
	const prefix = "env:"
	if len(value) > len(prefix) && value[:len(prefix)] == prefix {
		return os.Getenv(value[len(prefix):])
	}
	return value
}
