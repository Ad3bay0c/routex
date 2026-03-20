package agents

import (
	"strings"
	"testing"
)

func TestRole_IsValid(t *testing.T) {
	tests := []struct {
		role Role
		want bool
	}{
		{Planner, true},
		{Writer, true},
		{Critic, true},
		{Executor, true},
		{Researcher, true},
		{"spelunker", false}, // typo
		{"", false},          // empty
		{"PLANNER", false},   // wrong case — roles are lowercase
	}

	for _, tt := range tests {
		t.Run(string(tt.role), func(t *testing.T) {
			got := tt.role.IsValid()
			if got != tt.want {
				t.Errorf("Role(%q).IsValid() = %v, want %v", tt.role, got, tt.want)
			}
		})
	}
}

func TestRole_String(t *testing.T) {
	tests := []struct {
		role Role
		want string
	}{
		{Planner, "planner"},
		{Writer, "writer"},
		{Critic, "critic"},
		{Executor, "executor"},
		{Researcher, "researcher"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.role.String(); got != tt.want {
				t.Errorf("Role.String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRole_SystemPrompt_NotEmpty(t *testing.T) {
	roles := []Role{Planner, Writer, Critic, Executor, Researcher}

	for _, role := range roles {
		t.Run(role.String(), func(t *testing.T) {
			prompt := role.SystemPrompt()
			if strings.TrimSpace(prompt) == "" {
				t.Errorf("Role(%q).SystemPrompt() returned empty string", role)
			}
		})
	}
}

func TestRole_SystemPrompt_ContainsRoleIdentity(t *testing.T) {
	tests := []struct {
		role    Role
		keyword string
	}{
		{Planner, "plan"},
		{Writer, "writ"}, // matches "writing" or "writer"
		{Critic, "review"},
		{Executor, "execut"}, // matches "executor" or "execute"
		{Researcher, "research"},
	}

	for _, tt := range tests {
		t.Run(tt.role.String(), func(t *testing.T) {
			prompt := strings.ToLower(tt.role.SystemPrompt())
			if !strings.Contains(prompt, tt.keyword) {
				t.Errorf(
					"Role(%q).SystemPrompt() does not contain %q\ngot: %s",
					tt.role, tt.keyword, prompt,
				)
			}
		})
	}
}

func TestRole_DefaultSystemPrompt(t *testing.T) {
	unknown := Role("unknown_role")
	prompt := unknown.SystemPrompt()
	if strings.TrimSpace(prompt) == "" {
		t.Errorf("unknown role returned empty system prompt, want a default")
	}
}
