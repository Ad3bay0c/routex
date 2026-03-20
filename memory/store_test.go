package memory

import "testing"

func TestAgentKey(t *testing.T) {
	tests := []struct {
		agentID string
		suffix  string
		want    string
	}{
		{"writer", "draft", "agent:writer:draft"},
		{"planner", "history", "agent:planner:history"},
		{"critic", "output", "agent:critic:output"},
	}

	for _, tt := range tests {
		got := AgentKey(tt.agentID, tt.suffix)
		if got != tt.want {
			t.Errorf("AgentKey(%q, %q) = %q, want %q", tt.agentID, tt.suffix, got, tt.want)
		}
	}
}

func TestRunKey(t *testing.T) {
	got := RunKey("run123", "writer", "draft")
	want := "run:run123:writer:draft"
	if got != want {
		t.Errorf("RunKey() = %q, want %q", got, want)
	}
}
