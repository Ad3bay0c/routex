package mcp

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClient_Connect(t *testing.T) {
	srv := newTestServer(t, nil, nil)
	client := NewClient(srv.URL)

	if err := client.Connect(context.Background()); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
}

func TestClient_Connect_CapturesSessionID(t *testing.T) {
	// Server requires Mcp-Session-Id on all requests after initialize
	m := &mockMCPServer{
		toolList:       []mcpTool{{Name: "ping"}},
		callResults:    map[string]string{"ping": "pong"},
		requireSession: true,
	}
	srv := httptest.NewServer(m.handler())
	t.Cleanup(srv.Close)

	client := NewClient(srv.URL)

	// Connect should succeed — client captures the session ID from initialize response
	if err := client.Connect(context.Background()); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}

	if client.sessionID == "" {
		t.Error("client should have captured session ID from initialize response")
	}

	// tools/list should succeed — client sends the session ID automatically
	tools, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools() should succeed with session ID, got: %v", err)
	}
	if len(tools) != 1 {
		t.Errorf("expected 1 tool, got %d", len(tools))
	}
}

func TestClient_Connect_MissingSessionID_Fails(t *testing.T) {
	m := &mockMCPServer{
		toolList:       []mcpTool{{Name: "ping"}},
		requireSession: true,
	}
	srv := httptest.NewServer(m.handler())
	t.Cleanup(srv.Close)

	client := NewClient(srv.URL)
	_ = client.Connect(context.Background())

	client.sessionID = ""

	_, err := client.ListTools(context.Background())
	if err == nil {
		t.Fatal("ListTools() should fail without session ID")
	}
	if !strings.Contains(err.Error(), "Session-Id") {
		t.Errorf("error should mention Session-Id, got: %v", err)
	}
}

func TestClient_ListTools(t *testing.T) {
	toolList := []mcpTool{
		{
			Name:        "get_weather",
			Description: "Get current weather for a location",
			InputSchema: mcpInputSchema{
				Type: "object",
				Properties: map[string]mcpProperty{
					"location": {Type: "string", Description: "City name"},
				},
				Required: []string{"location"},
			},
		},
		{
			Name:        "search_web",
			Description: "Search the web",
			InputSchema: mcpInputSchema{
				Type: "object",
				Properties: map[string]mcpProperty{
					"query": {Type: "string", Description: "Search query"},
				},
			},
		},
	}

	srv := newTestServer(t, toolList, nil)
	client := NewClient(srv.URL)
	_ = client.Connect(context.Background())

	got, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools() error: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("ListTools() returned %d tools, want 2", len(got))
	}
	if got[0].Name != "get_weather" {
		t.Errorf("[0].Name = %q, want get_weather", got[0].Name)
	}
	if got[1].Name != "search_web" {
		t.Errorf("[1].Name = %q, want search_web", got[1].Name)
	}
	if len(got[0].InputSchema.Required) != 1 || got[0].InputSchema.Required[0] != "location" {
		t.Errorf("[0].Required = %v, want [location]", got[0].InputSchema.Required)
	}
}

func TestClient_CallTool(t *testing.T) {
	srv := newTestServer(t,
		[]mcpTool{{Name: "get_weather"}},
		map[string]string{"get_weather": "Sunny, 28°C in Lagos"},
	)
	client := NewClient(srv.URL)
	_ = client.Connect(context.Background())

	output, err := client.CallTool(context.Background(), "get_weather",
		json.RawMessage(`{"location":"Lagos"}`),
	)
	if err != nil {
		t.Fatalf("CallTool() error: %v", err)
	}
	if output != "Sunny, 28°C in Lagos" {
		t.Errorf("output = %q, want Sunny, 28°C in Lagos", output)
	}
}

func TestClient_CallTool_ServerError(t *testing.T) {
	m := &mockMCPServer{
		toolList:   []mcpTool{{Name: "bad_tool"}},
		callErrors: map[string]string{"bad_tool": "rate limit exceeded"},
	}
	srv := httptest.NewServer(m.handler())
	t.Cleanup(srv.Close)

	client := NewClient(srv.URL)
	_ = client.Connect(context.Background())

	_, err := client.CallTool(context.Background(), "bad_tool", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("CallTool() should error when server returns isError=true")
	}
	if !strings.Contains(err.Error(), "rate limit exceeded") {
		t.Errorf("error = %q, should contain error message", err.Error())
	}
}

func TestClient_NetworkError(t *testing.T) {
	client := NewClient("http://127.0.0.1:1")
	err := client.Connect(context.Background())
	if err == nil {
		t.Fatal("Connect() should error when server is unreachable")
	}
}
