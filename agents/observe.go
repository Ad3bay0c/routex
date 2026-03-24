package agents

import (
	"context"
	"time"
)

// AgentTracer is the tracing interface agents use.
// Implemented by *observe.Tracer — also by NoopTracer for when tracing is off.
type AgentTracer interface {
	StartAgent(ctx context.Context, agentID, role string) (context.Context, func(error))
	StartLLMCall(ctx context.Context, provider, model string) (context.Context, func(tokensUsed int, err error))
	StartToolCall(ctx context.Context, toolName, input string) (context.Context, func(output string, err error))
}

// AgentMetrics is the metrics interface agents use.
// Implemented by *observe.Metrics — also by NoopMetrics for when metrics are off.
type AgentMetrics interface {
	RecordTokens(agentID, provider string, count int)
	RecordToolCall(toolName string, duration time.Duration, err error)
	RecordAgentRun(agentID, role string, duration time.Duration)
	RecordAgentFailure(agentID string)
}

// noopTracer is the default when tracing is disabled.
// All methods return the context unchanged and a no-op finish func.
type noopTracer struct{}

func (noopTracer) StartAgent(ctx context.Context, _, _ string) (context.Context, func(error)) {
	return ctx, func(error) {}
}
func (noopTracer) StartLLMCall(ctx context.Context, _, _ string) (context.Context, func(int, error)) {
	return ctx, func(int, error) {}
}
func (noopTracer) StartToolCall(ctx context.Context, _, _ string) (context.Context, func(string, error)) {
	return ctx, func(string, error) {}
}

// noopMetrics is the default when metrics are disabled.
type noopMetrics struct{}

func (noopMetrics) RecordTokens(_, _ string, _ int)                   {}
func (noopMetrics) RecordToolCall(_ string, _ time.Duration, _ error) {}
func (noopMetrics) RecordAgentRun(_, _ string, _ time.Duration)       {}
func (noopMetrics) RecordAgentFailure(_ string)                       {}

// compile-time checks
var _ AgentTracer = noopTracer{}
var _ AgentMetrics = noopMetrics{}
