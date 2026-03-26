package mcp

// Package mcp provides MCP (Model Context Protocol) tool server support for Routex.
//
// # Configuration
//
// Configure in agents.yaml under the tools: section. Each MCP server is one
// entry — all tools it exposes are registered automatically:
//
//	tools:
//	  - name:    "mcp"
//	    extra:
//	      server_url:  "http://localhost:3000"   # required
//	      server_name: "github-tools"            # optional label for logs
//
// Multiple servers can be declared with different labels:
//
//	tools:
//	  - name: "mcp"
//	    extra:
//	      server_url:  "http://localhost:3000"
//	      server_name: "github"
//
//	  - name: "mcp"
//	    extra:
//	      server_url:  "http://localhost:3001"
//	      server_name: "postgres"
//
// # Tool naming
//
// Tools from an MCP server keep their original names (e.g. "get_weather").
// If two servers expose a tool with the same name, the server_name is used
// as a prefix: "github.get_weather" and "postgres.get_weather".
import (
	"context"
	"fmt"
	"log/slog"

	"github.com/Ad3bay0c/routex/tools"
)

// ServerConfig holds the configuration for one MCP server connection.
type ServerConfig struct {
	// ServerURL is the full URL of the MCP server endpoint.
	// Example: "http://localhost:3000"
	ServerURL string

	// ServerName is an optional human-readable label used in logs
	// and as a prefix when disambiguating tool name collisions.
	// If empty, the server URL is used.
	ServerName string

	// Headers are additional HTTP headers sent on every request to this server.
	// Use this to pass authentication tokens:
	//
	//   header_Authorization: "env:GITHUB_TOKEN"   → Authorization: ghp_xxx
	//   header_X-Api-Key:     "env:MY_KEY"         → X-Api-Key: abc123
	Headers map[string]string
}

// RegisterServer connects to the MCP server at cfg.ServerURL, fetches all
// its tools via the tools/list endpoint, and registers each one in registry.
func RegisterServer(ctx context.Context, cfg ServerConfig, registry *tools.Registry, logger *slog.Logger) ([]string, error) {
	name := cfg.ServerName
	if name == "" {
		name = cfg.ServerURL
	}

	logger = logger.With("mcp_server", name)
	logger.Info("connecting to MCP server", "url", cfg.ServerURL)

	var client *Client
	if len(cfg.Headers) > 0 {
		client = NewClientWithHeaders(cfg.ServerURL, cfg.Headers)
		logger.Debug("MCP client configured with auth headers",
			"header_count", len(cfg.Headers),
		)
	} else {
		client = NewClient(cfg.ServerURL)
	}

	// Perform the MCP initialization handshake
	if err := client.Connect(ctx); err != nil {
		return nil, fmt.Errorf("mcp: connect to %q: %w", name, err)
	}

	// Discover all tools the server exposes
	mcpTools, err := client.ListTools(ctx)
	if err != nil {
		return nil, fmt.Errorf("mcp: list tools from %q: %w", name, err)
	}

	if len(mcpTools) == 0 {
		logger.Warn("MCP server exposed no tools", "url", cfg.ServerURL)
		return nil, nil
	}

	// Detect name collisions — if the registry already has a tool with the
	// same name, prefix this one with the server name to disambiguate.
	var registered []string
	for _, mt := range mcpTools {
		toolName := mt.Name
		if registry.Has(toolName) {
			toolName = cfg.ServerName + "." + mt.Name
			logger.Warn("MCP tool name collision — using prefixed name",
				"original", mt.Name,
				"prefixed", toolName,
			)
		}

		// Translate MCP inputSchema to Routex tools.Schema
		schema := tools.Schema{
			Description: mt.Description,
			Parameters:  make(map[string]tools.Parameter, len(mt.InputSchema.Properties)),
		}
		required := make(map[string]bool, len(mt.InputSchema.Required))
		for _, r := range mt.InputSchema.Required {
			required[r] = true
		}
		for paramName, prop := range mt.InputSchema.Properties {
			schema.Parameters[paramName] = tools.Parameter{
				Type:        prop.Type,
				Description: prop.Description,
				Required:    required[paramName],
			}
		}

		t := &MCPTool{
			client:   client,
			toolName: toolName,
			schema:   schema,
		}

		registry.Register(t)
		registered = append(registered, toolName)
		logger.Info("MCP tool registered", "tool", toolName)
	}

	logger.Info("MCP server connected",
		"url", cfg.ServerURL,
		"tools", len(registered),
	)

	return registered, nil
}
