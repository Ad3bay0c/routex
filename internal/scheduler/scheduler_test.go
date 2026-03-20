package scheduler

import (
	"strings"
	"testing"
)

func TestBuildWaves_LinearChain(t *testing.T) {
	// planner → writer → critic : expected 3 waves of 1 agent each
	nodes := []agentNode{
		&nodeStub{"planner", nil},
		&nodeStub{"writer", []string{"planner"}},
		&nodeStub{"critic", []string{"writer"}},
	}

	waves, err := buildWavesFromNodes(nodes)
	if err != nil {
		t.Fatalf("buildWavesFromNodes() error: %v", err)
	}
	if len(waves) != 3 {
		t.Fatalf("expected 3 waves, got %d", len(waves))
	}
	assertWave(t, waves[0], "planner")
	assertWave(t, waves[1], "writer")
	assertWave(t, waves[2], "critic")
}

func TestBuildWaves_LinearChain_Unordered(t *testing.T) {
	nodes := []agentNode{
		&nodeStub{"critic", []string{"writer"}},
		&nodeStub{"planner", nil},
		&nodeStub{"writer", []string{"planner"}},
	}

	waves, err := buildWavesFromNodes(nodes)
	if err != nil {
		t.Fatalf("buildWavesFromNodes() error: %v", err)
	}
	if len(waves) != 3 {
		t.Fatalf("expected 3 waves, got %d", len(waves))
	}
	assertWave(t, waves[0], "planner")
	assertWave(t, waves[1], "writer")
	assertWave(t, waves[2], "critic")
}

func TestBuildWaves_Parallel(t *testing.T) {
	// researcher_a and researcher_b run in parallel (wave 1)
	// writer waits for both (wave 2)
	nodes := []agentNode{
		&nodeStub{"researcher_a", nil},
		&nodeStub{"researcher_b", nil},
		&nodeStub{"writer", []string{"researcher_a", "researcher_b"}},
	}

	waves, err := buildWavesFromNodes(nodes)
	if err != nil {
		t.Fatalf("buildWavesFromNodes() error: %v", err)
	}
	if len(waves) != 2 {
		t.Fatalf("expected 2 waves, got %d", len(waves))
	}
	if len(waves[0]) != 2 {
		t.Errorf("wave 1 should have 2 parallel agents, got %d", len(waves[0]))
	}
	assertWave(t, waves[0], "researcher_a")
	assertWave(t, waves[0], "researcher_b")
	assertWave(t, waves[1], "writer")
}

func TestBuildWaves_SingleAgent(t *testing.T) {
	nodes := []agentNode{&nodeStub{"solo", nil}}

	waves, err := buildWavesFromNodes(nodes)
	if err != nil {
		t.Fatalf("buildWavesFromNodes() error: %v", err)
	}
	if len(waves) != 1 || len(waves[0]) != 1 {
		t.Fatalf("expected 1 wave with 1 agent, got %d waves", len(waves))
	}
}

func TestValidateGraph_Cycle_Direct(t *testing.T) {
	// a → b → a
	nodes := []agentNode{
		&nodeStub{"a", []string{"b"}},
		&nodeStub{"b", []string{"a"}},
	}
	err := validateGraphNodes(nodes)
	if err == nil {
		t.Fatal("validateGraphNodes() should detect direct cycle a→b→a")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("error should mention 'cycle', got: %v", err)
	}
}

func TestValidateGraph_Cycle_ThreeNodes(t *testing.T) {
	// a → b → c → a
	nodes := []agentNode{
		&nodeStub{"a", []string{"b"}},
		&nodeStub{"b", []string{"c"}},
		&nodeStub{"c", []string{"a"}},
	}
	if err := validateGraphNodes(nodes); err == nil {
		t.Error("validateGraphNodes() should detect three-node cycle")
	}
}

func TestValidateGraph_MissingDependency(t *testing.T) {
	nodes := []agentNode{
		&nodeStub{"writer", []string{"planner"}}, // planner does not exist
	}
	if err := validateGraphNodes(nodes); err == nil {
		t.Error("validateGraphNodes() should error on missing dependency")
	}
}

func TestValidateGraph_Valid(t *testing.T) {
	nodes := []agentNode{
		&nodeStub{"planner", nil},
		&nodeStub{"writer", []string{"planner"}},
		&nodeStub{"critic", []string{"writer"}},
	}
	if err := validateGraphNodes(nodes); err != nil {
		t.Errorf("validateGraphNodes() on valid graph returned error: %v", err)
	}
}

func TestValidateGraph_NoDependencies(t *testing.T) {
	nodes := []agentNode{
		&nodeStub{"a", nil},
		&nodeStub{"b", nil},
		&nodeStub{"c", nil},
	}
	if err := validateGraphNodes(nodes); err != nil {
		t.Errorf("validateGraphNodes() on independent agents returned error: %v", err)
	}
}

func TestFormatCycle(t *testing.T) {
	tests := []struct {
		cycle []string
		want  string
	}{
		{[]string{"a", "b", "a"}, "a → b → a"},
		{[]string{"x", "y", "z", "x"}, "x → y → z → x"},
		{[]string{"solo"}, "solo"},
	}
	for _, tt := range tests {
		got := formatCycle(tt.cycle)
		if got != tt.want {
			t.Errorf("formatCycle(%v) = %q, want %q", tt.cycle, got, tt.want)
		}
	}
}

type nodeStub struct {
	id   string
	deps []string
}

func (n *nodeStub) ID() string          { return n.id }
func (n *nodeStub) DependsOn() []string { return n.deps }

func assertWave(t *testing.T, wave []agentNode, id string) {
	t.Helper()
	for _, n := range wave {
		if n.ID() == id {
			return
		}
	}
	t.Errorf("wave does not contain agent %q", id)
}
