package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Ad3bay0c/routex/tools"
)

type mockMCPServer struct {
	toolList       []mcpTool
	callResults    map[string]string
	callErrors     map[string]string
	requireSession bool
	sessionID      string
}

func (m *mockMCPServer) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if m.requireSession && r.Method == http.MethodPost {
			var peek struct {
				Method string `json:"method"`
			}
			body, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(body))
			json.Unmarshal(body, &peek)

			if peek.Method != "initialize" && r.Header.Get("Mcp-Session-Id") != m.sessionID {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(jsonRpcResponse{
					JSONRPC: "2.0",
					Error:   &jsonRpcError{Code: -32000, Message: "Bad Request: Mcp-Session-Id header is required"},
				})
				return
			}
		}
		var req jsonRpcRequest
		_ = json.NewDecoder(r.Body).Decode(&req)

		w.Header().Set("Content-Type", "application/json")

		switch req.Method {
		case "initialize":
			if m.requireSession {
				m.sessionID = "test-session-abc-123"
				w.Header().Set("Mcp-Session-Id", m.sessionID)
			}
			_ = json.NewEncoder(w).Encode(jsonRpcResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: mustMarshalRaw(map[string]any{
					"protocolVersion": protocolVersion,
					"capabilities":    map[string]any{"tools": map[string]any{}},
					"serverInfo":      map[string]any{"name": "mock-server", "version": "1.0.0"},
				}),
			})

		case "notifications/initialized":
			w.WriteHeader(http.StatusOK)

		case "tools/list":
			_ = json.NewEncoder(w).Encode(jsonRpcResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: mustMarshalRaw(map[string]any{
					"tools": m.toolList,
				}),
			})

		case "tools/call":
			var params struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			_ = json.Unmarshal(req.Params, &params)

			if errMsg, ok := m.callErrors[params.Name]; ok {
				_ = json.NewEncoder(w).Encode(jsonRpcResponse{
					JSONRPC: "2.0",
					ID:      req.ID,
					Result: mustMarshalRaw(mcpToolResult{
						Content: []mcpContent{{Type: "text", Text: errMsg}},
						IsError: true,
					}),
				})
				return
			}

			output := m.callResults[params.Name]
			if output == "" {
				output = "result from " + params.Name
			}
			_ = json.NewEncoder(w).Encode(jsonRpcResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: mustMarshalRaw(mcpToolResult{
					Content: []mcpContent{{Type: "text", Text: output}},
					IsError: false,
				}),
			})

		default:
			_ = json.NewEncoder(w).Encode(jsonRpcResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &jsonRpcError{Code: -32601, Message: "method not found: " + req.Method},
			})
		}
	}
}

// newTestServer starts a mock MCP server and returns it and its URL.
func newTestServer(t *testing.T, toolList []mcpTool, callResults map[string]string) *httptest.Server {
	t.Helper()
	m := &mockMCPServer{
		toolList:    toolList,
		callResults: callResults,
	}
	srv := httptest.NewServer(m.handler())
	t.Cleanup(srv.Close)
	return srv
}

func mustMarshalRaw(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func TestMCPTool_Execute(t *testing.T) {
	srv := newTestServer(t,
		[]mcpTool{{Name: "get_weather"}},
		map[string]string{"get_weather": "28°C, sunny"},
	)

	client := NewClient(srv.URL)
	_ = client.Connect(context.Background())

	tool := &MCPTool{
		client:   client,
		toolName: "get_weather",
		schema:   tools.Schema{Description: "Get weather"},
	}

	result, err := tool.Execute(context.Background(),
		json.RawMessage(`{"location":"Lagos"}`),
	)
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	var out map[string]string
	_ = json.Unmarshal(result, &out)
	if out["output"] != "28°C, sunny" {
		t.Errorf("output = %q, want 28°C, sunny", out["output"])
	}
}

func TestMCPTool_Execute_EmptyInput(t *testing.T) {
	srv := newTestServer(t,
		[]mcpTool{{Name: "ping"}},
		map[string]string{"ping": "pong"},
	)
	client := NewClient(srv.URL)
	_ = client.Connect(context.Background())

	tool := &MCPTool{client: client, toolName: "ping"}

	_, err := tool.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("Execute() with nil input error: %v", err)
	}
}

func TestMCPTool_NameAndSchema(t *testing.T) {
	tool := &MCPTool{
		toolName: "my_tool",
		schema:   tools.Schema{Description: "does things"},
	}
	if tool.Name() != "my_tool" {
		t.Errorf("Name() = %q, want my_tool", tool.Name())
	}
	if tool.Schema().Description != "does things" {
		t.Errorf("Schema().Description = %q, want 'does things'", tool.Schema().Description)
	}
}

type fakeBuiltin struct{ name string }

func (f *fakeBuiltin) Name() string         { return f.name }
func (f *fakeBuiltin) Schema() tools.Schema { return tools.Schema{} }
func (f *fakeBuiltin) Execute(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
	return nil, nil
}
