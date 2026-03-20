package tools

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

type mockTool struct {
	name   string
	schema Schema
	output json.RawMessage
	err    error
}

func (m *mockTool) Name() string   { return m.name }
func (m *mockTool) Schema() Schema { return m.schema }
func (m *mockTool) Execute(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
	return m.output, m.err
}

func TestRegistry_Register_And_Get(t *testing.T) {
	r := NewRegistry()
	tool := &mockTool{name: "mock_tool"}

	r.Register(tool)

	got, ok := r.Get("mock_tool")
	if !ok {
		t.Fatal("Get() returned false for registered tool")
	}
	if got.Name() != "mock_tool" {
		t.Errorf("Get() returned wrong tool: %q", got.Name())
	}
}

func TestRegistry_Get_NotFound(t *testing.T) {
	r := NewRegistry()

	_, ok := r.Get("nonexistent")
	if ok {
		t.Error("Get() returned true for unregistered tool")
	}
}

func TestRegistry_Register_Overwrites(t *testing.T) {
	r := NewRegistry()

	r.Register(&mockTool{name: "tool", schema: Schema{Description: "first"}})
	r.Register(&mockTool{name: "tool", schema: Schema{Description: "second"}})

	got, _ := r.Get("tool")
	if got.Schema().Description != "second" {
		t.Errorf("second registration should overwrite first, got %q", got.Schema().Description)
	}
}

func TestRegistry_List(t *testing.T) {
	r := NewRegistry()
	r.Register(&mockTool{name: "tool_a"})
	r.Register(&mockTool{name: "tool_b"})
	r.Register(&mockTool{name: "tool_c"})

	names := r.List()
	if len(names) != 3 {
		t.Errorf("List() returned %d names, want 3", len(names))
	}

	// Verify all names are present (order is not guaranteed — maps in Go are unordered)
	nameSet := make(map[string]bool)
	for _, n := range names {
		nameSet[n] = true
	}
	for _, want := range []string{"tool_a", "tool_b", "tool_c"} {
		if !nameSet[want] {
			t.Errorf("List() missing %q", want)
		}
	}
}

func TestRegistry_Execute_Success(t *testing.T) {
	r := NewRegistry()
	expected := json.RawMessage(`{"result":"ok"}`)
	r.Register(&mockTool{
		name:   "my_tool",
		output: expected,
	})

	got, err := r.Execute(context.Background(), "my_tool", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	if string(got) != string(expected) {
		t.Errorf("Execute() = %s, want %s", got, expected)
	}
}

func TestRegistry_Execute_ToolError(t *testing.T) {
	r := NewRegistry()
	toolErr := errors.New("tool exploded")
	r.Register(&mockTool{name: "bad_tool", err: toolErr})

	_, err := r.Execute(context.Background(), "bad_tool", json.RawMessage(`{}`))
	if !errors.Is(err, toolErr) {
		t.Errorf("Execute() should propagate tool error, got: %v", err)
	}
}

func TestRegistry_Execute_NotRegistered(t *testing.T) {
	r := NewRegistry()

	_, err := r.Execute(context.Background(), "ghost_tool", json.RawMessage(`{}`))
	if err == nil {
		t.Error("Execute() on unregistered tool should error")
	}
}

func TestRegistry_Schemas(t *testing.T) {
	r := NewRegistry()
	r.Register(&mockTool{
		name: "search",
		schema: Schema{
			Description: "Search the web",
			Parameters: map[string]Parameter{
				"query": {Type: "string", Required: true},
			},
		},
	})

	schemas := r.Schemas()
	if len(schemas) != 1 {
		t.Fatalf("Schemas() returned %d schemas, want 1", len(schemas))
	}

	s, ok := schemas["search"]
	if !ok {
		t.Fatal("Schemas() missing 'search'")
	}
	if s.Description != "Search the web" {
		t.Errorf("Schema description = %q, want %q", s.Description, "Search the web")
	}
}

func TestToolCall_ZeroValue(t *testing.T) {
	var tc ToolCall
	if tc.ToolName != "" {
		t.Error("zero-value ToolCall.ToolName should be empty")
	}
	if tc.Duration != 0 {
		t.Error("zero-value ToolCall.Duration should be 0")
	}
	if tc.Error != nil {
		t.Error("zero-value ToolCall.Error should be nil")
	}
}

func TestToolCall_WithDuration(t *testing.T) {
	tc := ToolCall{
		ToolName: "web_search",
		Duration: 250 * time.Millisecond,
	}
	if tc.Duration != 250*time.Millisecond {
		t.Errorf("Duration = %v, want 250ms", tc.Duration)
	}
}
