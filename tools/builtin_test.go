package tools

import (
	"errors"
	"os"
	"slices"
	"strings"
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

func TestListBuiltins_IncludesRegistered(t *testing.T) {
	const name = "list_builtins_probe_xyz"
	RegisterBuiltin(name, func(cfg ToolConfig) (Tool, error) {
		return &mockTool{name: name}, nil
	})
	names := ListBuiltins()
	if !slices.Contains(names, name) {
		t.Errorf("ListBuiltins() missing %q in %v", name, names)
	}
}

func TestErrToolNotBuiltin_ErrorString(t *testing.T) {
	err := ErrToolNotBuiltin{Name: "my_tool"}
	s := err.Error()
	if !strings.Contains(s, "my_tool") || !strings.Contains(s, "built-in") || !strings.Contains(s, "RegisterTool") {
		t.Errorf("Error() = %q", s)
	}
}

func TestSchemaForBuiltin(t *testing.T) {
	const okName = "schema_builtin_ok"
	RegisterBuiltin(okName, func(cfg ToolConfig) (Tool, error) {
		return &mockTool{
			name: okName,
			schema: Schema{
				Description: "test schema",
				Parameters: map[string]Parameter{
					"x": {Type: "string", Description: "param", Required: true},
				},
			},
		}, nil
	})

	s, ok := SchemaForBuiltin(okName)
	if !ok {
		t.Fatal("SchemaForBuiltin: want ok")
	}
	if s.Description != "test schema" {
		t.Errorf("Description = %q", s.Description)
	}
	if s.Parameters["x"].Required != true {
		t.Errorf("parameter x: %+v", s.Parameters["x"])
	}

	_, ok = SchemaForBuiltin("no_such_builtin_tool_ever")
	if ok {
		t.Error("unknown builtin should return !ok")
	}

	const failName = "schema_builtin_fail"
	RegisterBuiltin(failName, func(cfg ToolConfig) (Tool, error) {
		return nil, errors.New("no key")
	})
	_, ok = SchemaForBuiltin(failName)
	if ok {
		t.Error("factory error should return !ok")
	}
}
