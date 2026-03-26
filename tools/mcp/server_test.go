package mcp

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/Ad3bay0c/routex/tools"
)

func TestRegisterServer_RegistersAllTools(t *testing.T) {
	toolList := []mcpTool{
		{
			Name:        "get_weather",
			Description: "Get weather data",
			InputSchema: mcpInputSchema{
				Type: "object",
				Properties: map[string]mcpProperty{
					"location": {Type: "string", Description: "City"},
				},
				Required: []string{"location"},
			},
		},
		{
			Name:        "search_repos",
			Description: "Search GitHub repos",
			InputSchema: mcpInputSchema{
				Type: "object",
				Properties: map[string]mcpProperty{
					"query": {Type: "string", Description: "Search query"},
				},
			},
		},
	}

	srv := newTestServer(t, toolList, nil)
	registry := tools.NewRegistry()
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	registered, err := RegisterServer(context.Background(), ServerConfig{
		ServerURL:  srv.URL,
		ServerName: "test-server",
	}, registry, logger)

	if err != nil {
		t.Fatalf("RegisterServer() error: %v", err)
	}
	if len(registered) != 2 {
		t.Fatalf("registered %d tools, want 2", len(registered))
	}

	// Both tools should be in the registry
	for _, name := range []string{"get_weather", "search_repos"} {
		if !registry.Has(name) {
			t.Errorf("registry missing tool %q", name)
		}
	}
}

func TestRegisterServer_SchemaTranslation(t *testing.T) {
	toolList := []mcpTool{
		{
			Name:        "weather",
			Description: "Get weather",
			InputSchema: mcpInputSchema{
				Type: "object",
				Properties: map[string]mcpProperty{
					"city":  {Type: "string", Description: "The city name"},
					"units": {Type: "string", Description: "metric or imperial"},
				},
				Required: []string{"city"},
			},
		},
	}

	srv := newTestServer(t, toolList, nil)
	registry := tools.NewRegistry()
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	RegisterServer(context.Background(), ServerConfig{ServerURL: srv.URL}, registry, logger)

	tool, ok := registry.Get("weather")
	if !ok {
		t.Fatal("weather tool not registered")
	}

	schema := tool.Schema()
	if schema.Description != "Get weather" {
		t.Errorf("Description = %q, want Get weather", schema.Description)
	}

	cityParam, ok := schema.Parameters["city"]
	if !ok {
		t.Fatal("schema missing 'city' parameter")
	}
	if cityParam.Type != "string" {
		t.Errorf("city.Type = %q, want string", cityParam.Type)
	}
	if !cityParam.Required {
		t.Error("city should be Required=true")
	}

	unitsParam, ok := schema.Parameters["units"]
	if !ok {
		t.Fatal("schema missing 'units' parameter")
	}
	if unitsParam.Required {
		t.Error("units should be Required=false")
	}
}

func TestRegisterServer_NameCollision_PrefixesName(t *testing.T) {
	toolList := []mcpTool{
		{Name: "web_search", Description: "MCP web search"},
	}

	srv := newTestServer(t, toolList, nil)
	registry := tools.NewRegistry()
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	// Pre-register a built-in with the same name
	registry.Register(&fakeBuiltin{name: "web_search"})

	registered, err := RegisterServer(context.Background(), ServerConfig{
		ServerURL:  srv.URL,
		ServerName: "mcp-server",
	}, registry, logger)

	if err != nil {
		t.Fatalf("RegisterServer() error: %v", err)
	}

	// Should be registered with prefixed name
	if len(registered) != 1 || registered[0] != "mcp-server.web_search" {
		t.Errorf("registered = %v, want [mcp-server.web_search]", registered)
	}
	if !registry.Has("mcp-server.web_search") {
		t.Error("registry should contain prefixed name mcp-server.web_search")
	}
}

func TestRegisterServer_NoTools(t *testing.T) {
	srv := newTestServer(t, nil, nil)
	registry := tools.NewRegistry()
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	registered, err := RegisterServer(context.Background(), ServerConfig{
		ServerURL: srv.URL,
	}, registry, logger)

	if err != nil {
		t.Fatalf("RegisterServer() should not error for empty tool list, got: %v", err)
	}
	if len(registered) != 0 {
		t.Errorf("registered = %v, want empty", registered)
	}
}
