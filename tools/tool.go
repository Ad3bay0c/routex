package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// Tool is the contract every tool in Routex must satisfy.
//
// To create a custom tool:
//  1. Create a struct
//  2. Add Name(), Schema(), and Execute() methods
//  3. Call rt.RegisterTool(&YourTool{})
type Tool interface {
	// Name returns the unique identifier for this tool.
	// This is what the LLM uses to call it, and what you write
	// in agents.yaml under tools: ["web_search"].
	// Keep it lowercase with underscores. No spaces.
	// Example: "web_search", "write_file", "db_query"
	Name() string

	// Schema describes the tool to the LLM so it knows:
	//   - what the tool does
	//   - what inputs it expects
	//   - what it returns
	// The LLM reads this and decides when and how to call the tool.
	// Think of it as the tool's instruction manual for the AI.
	Schema() Schema

	// Execute is called when the LLM decides to use this tool.
	// input is the raw JSON the LLM produced — your tool parses it.
	// The return value is raw JSON sent back to the LLM as the tool result.
	// ctx carries the deadline and cancellation from the runtime.
	// If Execute returns an error, the agent logs it and may retry.
	Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error)
}

// Schema describes a tool to the LLM.
// The runtime serialises this into the function-calling format
// that Anthropic, OpenAI, and Ollama all understand.
//
// You fill this out once per tool. The LLM reads it at runtime
// to understand what inputs your tool accepts.
type Schema struct {
	// Description is a plain-English explanation of what the tool does.
	// Write this as if explaining to a smart colleague, not a machine.
	// The LLM uses this to decide WHEN to call your tool.
	// Bad:  "searches"
	// Good: "Search the web and return the top results for a given query"
	Description string

	// Parameters describes the JSON object the LLM must send to Execute().
	// Each key is a parameter name, each value describes that parameter.
	// The LLM will construct the input JSON to match this shape.
	Parameters map[string]Parameter
}

// Parameter describes a single input field in a tool's Schema.
type Parameter struct {
	// Type is the JSON type: "string", "number", "boolean", "array", "object"
	Type string

	// Description tells the LLM what this field is for.
	// Be specific — the LLM fills this in based on your description.
	Description string

	// Required marks whether the LLM must include this field.
	// Missing required fields cause the tool call to be rejected.
	Required bool
}

// ToolCall is a record of a single tool execution by an agent.
// Stored in AgentResult.ToolCalls so you can see exactly what the agent did,
// what input it sent, what came back, and how long it took.
type ToolCall struct {
	// ToolName matches the name returned by Tool.Name().
	ToolName string

	// Input is the raw JSON the LLM passed to Tool.Execute().
	Input string

	// Output is the raw JSON Tool.Execute() returned.
	// Empty if the tool call failed.
	Output string

	// Duration is how long Tool.Execute() took to complete.
	Duration time.Duration

	// Error is non-nil if Tool.Execute() returned an error.
	Error error
}

// Registry holds all tools that have been registered with the runtime.
// Agents look up tools here by name when the LLM requests a tool call.
// You never interact with Registry directly — use rt.RegisterTool() instead.
type Registry struct {
	tools map[string]Tool
}

// NewRegistry creates an empty tool registry.
func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]Tool),
	}
}

// Register adds a tool to the registry.
// If a tool with the same name already exists, it is replaced.
// This is called by rt.RegisterTool() — you rarely call it directly.
func (r *Registry) Register(t Tool) {
	r.tools[t.Name()] = t
}

// Get retrieves a tool by name.
// Returns the tool and true if found, nil and false if not.
// Agents call this when the LLM returns a tool_use block.
func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// Has reports whether a tool with the given name is registered.
func (r *Registry) Has(name string) bool {
	_, ok := r.tools[name]
	return ok
}

// List returns the names of all registered tools.
// The runtime passes these to the LLM at the start of each agent run
// so the model knows what it has available.
func (r *Registry) List() []string {
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	return names
}

// Schemas returns the Schema for every registered tool.
// The runtime serialises these into the format each LLM provider expects.
func (r *Registry) Schemas() map[string]Schema {
	schemas := make(map[string]Schema, len(r.tools))
	for name, tool := range r.tools {
		schemas[name] = tool.Schema()
	}
	return schemas
}

// Execute runs a tool by name with the given input.
// Called by the agent's goroutine when the LLM requests a tool call.
// Returns a clear error if the tool is not registered — agents log this
// and report it back to the LLM so it can try something else.
func (r *Registry) Execute(ctx context.Context, name string, input json.RawMessage) (json.RawMessage, error) {
	t, ok := r.tools[name]
	if !ok {
		return nil, fmt.Errorf("tool %q is not registered — did you call rt.RegisterTool()", name)
	}
	return t.Execute(ctx, input)
}
