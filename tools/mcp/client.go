package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"time"
)

// protocolVersion is the MCP spec version this client targets.
const protocolVersion = "2025-11-25"

type jsonRpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonRpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRpcError   `json:"error,omitempty"`
}

type jsonRpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *jsonRpcError) Error() string {
	return fmt.Sprintf("MCP error %d: %s", e.Code, e.Message)
}

// mcpTool is the shape of one tool in a tools/list response.
type mcpTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema mcpInputSchema `json:"inputSchema"`
}

// mcpInputSchema is the JSON Schema the MCP server sends for tool parameters.
type mcpInputSchema struct {
	Type       string                 `json:"type"`
	Properties map[string]mcpProperty `json:"properties,omitempty"`
	Required   []string               `json:"required,omitempty"`
}

type mcpProperty struct {
	Type        string `json:"type"`
	Description string `json:"description"`
}

// mcpToolResult is the shape of a tools/call response.
type mcpToolResult struct {
	Content []mcpContent `json:"content"`
	IsError bool         `json:"isError"`
}

type mcpContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// Client is a minimal MCP HTTP client.
// It handles the initialization handshake and exposes ListTools and CallTool.
// All requests are plain HTTP POST — no SSE streaming needed for tool calls.
type Client struct {
	serverURL string
	http      *http.Client
	headers   map[string]string // sent on every request (auth, etc.)
	sessionID string            // Mcp-Session-Id assigned by server during initialize
	idSeq     atomic.Int64
}

// NewClient creates a new MCP client pointed at serverURL.
// Call Connect() before ListTools or CallTool.
func NewClient(serverURL string) *Client {
	return &Client{
		serverURL: serverURL,
		http:      &http.Client{Timeout: 30 * time.Second},
		headers:   make(map[string]string),
	}
}

// NewClientWithHeaders creates a client with default headers sent on every request.
// Use this to pass authentication tokens required by the MCP server:
//
//	client := mcp.NewClientWithHeaders(url, map[string]string{
//	    "Authorization": "Bearer " + token,
//	    "X-Api-Key":     apiKey,
//	})
func NewClientWithHeaders(serverURL string, headers map[string]string) *Client {
	c := NewClient(serverURL)
	for k, v := range headers {
		c.headers[k] = v
	}
	return c
}

// Connect performs the MCP initialization handshake.
func (c *Client) Connect(ctx context.Context) error {
	type initParams struct {
		ProtocolVersion string `json:"protocolVersion"`
		Capabilities    struct {
		} `json:"capabilities"`
		ClientInfo struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"clientInfo"`
	}

	params := initParams{ProtocolVersion: protocolVersion}
	params.ClientInfo.Name = "routex"
	params.ClientInfo.Version = "1.0.0"

	var initResult struct {
		ProtocolVersion string `json:"protocolVersion"`
		Capabilities    struct {
			Tools *struct{} `json:"tools"`
		} `json:"capabilities"`
		ServerInfo struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
	}

	// rpcWithHeaders returns response headers so we can capture Mcp-Session-Id
	respHeaders, err := c.rpcCaptureHeaders(ctx, "initialize", params, &initResult)
	if err != nil {
		return fmt.Errorf("mcp: initialize: %w", err)
	}

	// Capture the session ID — required by servers that implement session management.
	// Once captured, it is sent as a header on every subsequent request.
	sid := respHeaders.Get("Mcp-Session-Id")
	if sid == "" {
		sid = respHeaders.Get("mcp-session-id")
	}
	if sid != "" {
		c.sessionID = sid
	}

	// send initialized notification (no response expected)
	if err := c.notify(ctx, "notifications/initialized", nil); err != nil {
		return fmt.Errorf("mcp: initialized notification: %w", err)
	}

	return nil
}

// ListTools calls tools/list and returns all tools the server exposes.
// Handles pagination automatically — fetches all pages.
func (c *Client) ListTools(ctx context.Context) ([]mcpTool, error) {
	type listParams struct {
		Cursor string `json:"cursor,omitempty"`
	}
	type listResult struct {
		Tools      []mcpTool `json:"tools"`
		NextCursor string    `json:"nextCursor,omitempty"`
	}

	var all []mcpTool
	cursor := ""

	for {
		var result listResult
		if err := c.rpc(ctx, "tools/list", listParams{Cursor: cursor}, &result); err != nil {
			return nil, fmt.Errorf("mcp: tools/list: %w", err)
		}
		all = append(all, result.Tools...)
		if result.NextCursor == "" {
			break
		}
		cursor = result.NextCursor
	}

	return all, nil
}

// CallTool calls tools/call with the given tool name and arguments.
// Returns the result as a JSON string the agent can read.
func (c *Client) CallTool(ctx context.Context, name string, arguments json.RawMessage) (string, error) {
	type callParams struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}

	var result mcpToolResult
	if err := c.rpc(ctx, "tools/call", callParams{Name: name, Arguments: arguments}, &result); err != nil {
		return "", fmt.Errorf("mcp: tools/call %q: %w", name, err)
	}

	// Collect all text content blocks into one string
	var parts []string
	for _, content := range result.Content {
		if content.Type == "text" && content.Text != "" {
			parts = append(parts, content.Text)
		}
	}

	output := joinStrings(parts, "\n")
	if result.IsError {
		return "", fmt.Errorf("mcp tool %q returned error: %s", name, output)
	}
	return output, nil
}

// rpc sends a JSON-RPC request and decodes the result into dest.
func (c *Client) rpc(ctx context.Context, method string, params any, dest any) error {
	_, err := c.rpcCaptureHeaders(ctx, method, params, dest)
	return err
}

// rpcCaptureHeaders is like rpc but also returns the HTTP response headers.
// Used by Connect() to capture Mcp-Session-Id from the initialize response.
func (c *Client) rpcCaptureHeaders(ctx context.Context, method string, params any, dest any) (http.Header, error) {
	id := c.idSeq.Add(1)

	var rawParams json.RawMessage
	if params != nil {
		p, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("marshal params: %w", err)
		}
		rawParams = p
	}

	req := jsonRpcRequest{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  method,
		Params:  rawParams,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.serverURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("MCP-Protocol-Version", protocolVersion)

	// Apply session ID on all requests after initialize
	if c.sessionID != "" {
		httpReq.Header.Set("Mcp-Session-Id", c.sessionID)
	}

	// Apply per-server auth headers (Authorization, X-Api-Key, etc.)
	for k, v := range c.headers {
		httpReq.Header.Set(k, v)
	}

	httpResp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", httpResp.StatusCode, string(respBody))
	}

	var rpcResp jsonRpcResponse
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if rpcResp.Error != nil {
		return nil, rpcResp.Error
	}

	if dest != nil && rpcResp.Result != nil {
		if err := json.Unmarshal(rpcResp.Result, dest); err != nil {
			return nil, fmt.Errorf("decode result: %w", err)
		}
	}

	return httpResp.Header, nil
}

// notify sends a JSON-RPC notification (no ID, no response expected).
func (c *Client) notify(ctx context.Context, method string, params any) error {
	var rawParams json.RawMessage
	if params != nil {
		p, err := json.Marshal(params)
		if err != nil {
			return err
		}
		rawParams = p
	}

	req := jsonRpcRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  rawParams,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.serverURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("MCP-Protocol-Version", protocolVersion)

	// Apply session ID — required after initialize on servers with session management
	if c.sessionID != "" {
		httpReq.Header.Set("Mcp-Session-Id", c.sessionID)
	}

	// Apply per-server auth headers
	for k, v := range c.headers {
		httpReq.Header.Set(k, v)
	}

	httpResp, err := c.http.Do(httpReq)
	if err != nil {
		return err
	}
	httpResp.Body.Close()
	return nil
}

func joinStrings(ss []string, sep string) string {
	if len(ss) == 0 {
		return ""
	}
	result := ss[0]
	for _, s := range ss[1:] {
		result += sep + s
	}
	return result
}
