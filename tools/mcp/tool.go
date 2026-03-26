package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Ad3bay0c/routex/tools"
)

// MCPTool wraps a single tool exposed by an MCP server.
// It satisfies the tools.Tool interface so it can be registered
// in the Routex tool registry and called by agents like any built-in.
type MCPTool struct {
	client   *Client
	toolName string // the name as declared by the MCP server
	schema   tools.Schema
}

func (t *MCPTool) Name() string         { return t.toolName }
func (t *MCPTool) Schema() tools.Schema { return t.schema }

// Execute proxies the call to the MCP server via tools/call.
// The input is passed as-is to the server's arguments field.
func (t *MCPTool) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	// Parse input to extract arguments — the agent sends a JSON object
	var args json.RawMessage
	if len(input) > 0 && string(input) != "null" {
		args = input
	} else {
		args = json.RawMessage("{}")
	}

	output, err := t.client.CallTool(ctx, t.toolName, args)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", t.toolName, err)
	}

	// Return the output as a JSON string — agents can read it directly
	result, err := json.Marshal(map[string]string{"output": output})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// compile-time check
var _ tools.Tool = (*MCPTool)(nil)
