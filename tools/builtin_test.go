package tools

import (
	"errors"
	"os"
	"testing"
)

func TestRegisterBuiltin_And_Resolve(t *testing.T) {
	// Register a test tool factory
	RegisterBuiltin("test_builtin", func(cfg ToolConfig) (Tool, error) {
		return &mockTool{name: "test_builtin"}, nil
	})

	tool, err := Resolve("test_builtin", ToolConfig{Name: "test_builtin"})
	if err != nil {
		t.Fatalf("Resolve() error: %v", err)
	}
	if tool.Name() != "test_builtin" {
		t.Errorf("Resolve() returned wrong tool: %q", tool.Name())
	}
}

func TestResolve_NotBuiltin(t *testing.T) {
	_, err := Resolve("definitely_not_a_builtin", ToolConfig{})

	// Must return ErrToolNotBuiltin specifically
	var notBuiltin ErrToolNotBuiltin
	if !errors.As(err, &notBuiltin) {
		t.Errorf("Resolve() on unknown tool should return ErrToolNotBuiltin, got: %T %v", err, err)
	}
	if notBuiltin.Name != "definitely_not_a_builtin" {
		t.Errorf("ErrToolNotBuiltin.Name = %q, want %q", notBuiltin.Name, "definitely_not_a_builtin")
	}
}

func TestResolve_FactoryError(t *testing.T) {
	// Register a factory that always fails
	RegisterBuiltin("broken_builtin", func(cfg ToolConfig) (Tool, error) {
		return nil, errors.New("factory failed")
	})

	_, err := Resolve("broken_builtin", ToolConfig{})
	if err == nil {
		t.Error("Resolve() should propagate factory errors")
	}
}

func TestListBuiltins_ContainsDefaults(t *testing.T) {
	// The three built-in tools register themselves via init()
	// They must all appear in ListBuiltins()
	names := ListBuiltins()

	nameSet := make(map[string]bool)
	for _, n := range names {
		nameSet[n] = true
	}

	for _, want := range []string{"web_search", "read_url", "write_file"} {
		if !nameSet[want] {
			t.Errorf("ListBuiltins() missing built-in %q", want)
		}
	}
}

func TestToolConfig_ResolveEnvValue(t *testing.T) {
	_ = os.Setenv("ROUTEX_INPUT", "How do I resolve env?")
	tests := []struct {
		input string
		want  string
	}{
		{"plain-value", "plain-value"},
		{"env:ROUTEX_INPUT", "How do I resolve env?"},
		{"env:ROUTEX_NO_EXIST", ""},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			// resolveEnvValue must not panic on any input
			got := resolveEnvValue(tt.input)
			if got != tt.want {
				t.Errorf("resolveEnvValue(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
