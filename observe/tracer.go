package observe

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/trace"
)

// TracerName is the instrumentation scope name used for all spans.
const TracerName = "routex"

// Tracer wraps the OpenTelemetry TracerProvider.
type Tracer struct {
	provider *sdktrace.TracerProvider
	tracer   trace.Tracer
}

// NewTracer creates and registers a TracerProvider that exports spans to
// the given OTLP HTTP endpoint (e.g. "http://localhost:4318").
//
//	http://localhost:4318   (OTLP HTTP)
//	http://localhost:16686  (Jaeger UI)
//
// Call Shutdown() when the runtime stops to flush any pending spans.
func NewTracer(ctx context.Context, serviceName, endpoint string) (*Tracer, error) {
	if endpoint == "" {
		return nil, fmt.Errorf("observe: tracer endpoint is required")
	}

	// OTLP HTTP exporter — works with Jaeger, Grafana Tempo, Honeycomb, etc.
	exporter, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpointURL(endpoint),
		otlptracehttp.WithInsecure(),
	)
	if err != nil {
		return nil, fmt.Errorf("observe: create OTLP exporter: %w", err)
	}

	// Resource describes this service to the backend
	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(serviceName),
			semconv.ServiceVersion(serviceVersion()),
			attribute.String("routex.component", "runtime"),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("observe: create resource: %w", err)
	}

	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		// Sample every trace in development.
		// In production, set via OTEL_TRACES_SAMPLER env var.
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)

	otel.SetTracerProvider(provider)

	return &Tracer{
		provider: provider,
		tracer:   provider.Tracer(TracerName),
	}, nil
}

// NewNoopTracer returns a tracer that does nothing.
// Used when tracing is disabled in config
func NewNoopTracer() *Tracer {
	return &Tracer{
		tracer: otel.GetTracerProvider().Tracer(TracerName),
	}
}

// Shutdown flushes pending spans and shuts down the exporter.
func (t *Tracer) Shutdown(ctx context.Context) error {
	if t == nil || t.provider == nil {
		return nil
	}
	return t.provider.Shutdown(ctx)
}

// StartRun starts a root span for an entire crew run.
// Returns the child context (passed to all agent spans) and a finish func.
//
//	ctx, finish := tracer.StartRun(ctx, runID, taskInput)
//	defer finish(err)
func (t *Tracer) StartRun(ctx context.Context, runID, taskInput string) (context.Context, func(error)) {
	if t == nil || t.tracer == nil {
		return ctx, func(error) {}
	}
	ctx, span := t.tracer.Start(ctx, "routex.run",
		trace.WithSpanKind(trace.SpanKindInternal),
	)
	span.SetAttributes(
		attribute.String("routex.run_id", runID),
		attribute.Int("routex.task.input_len", len(taskInput)),
	)
	return ctx, func(err error) {
		setSpanError(span, err)
		span.End()
	}
}

// StartAgent starts a span for a single agent execution.
// Call inside the agent's process() method.
//
//	ctx, finish := tracer.StartAgent(ctx, agentID, role)
//	defer finish(err)
func (t *Tracer) StartAgent(ctx context.Context, agentID, role string) (context.Context, func(error)) {
	if t == nil || t.tracer == nil {
		return ctx, func(error) {}
	}
	ctx, span := t.tracer.Start(ctx, "routex.agent",
		trace.WithSpanKind(trace.SpanKindInternal),
	)
	span.SetAttributes(
		attribute.String("routex.agent.id", agentID),
		attribute.String("routex.agent.role", role),
	)
	return ctx, func(err error) {
		setSpanError(span, err)
		span.End()
	}
}

// StartLLMCall starts a span for a single LLM completion call.
// Call inside the agent's think() loop.
//
//	ctx, finish := tracer.StartLLMCall(ctx, provider, model)
//	defer finish(err)
func (t *Tracer) StartLLMCall(ctx context.Context, provider, model string) (context.Context, func(tokensUsed int, err error)) {
	if t == nil || t.tracer == nil {
		return ctx, func(int, error) {}
	}
	ctx, span := t.tracer.Start(ctx, "routex.llm.complete",
		trace.WithSpanKind(trace.SpanKindClient),
	)
	span.SetAttributes(
		attribute.String("llm.provider", provider),
		attribute.String("llm.model", model),
	)
	return ctx, func(tokensUsed int, err error) {
		span.SetAttributes(attribute.Int("llm.tokens_used", tokensUsed))
		setSpanError(span, err)
		span.End()
	}
}

// StartToolCall starts a span for a single tool execution.
// Call inside the agent's think() loop when executing a tool.
//
//	ctx, finish := tracer.StartToolCall(ctx, toolName, input)
//	defer finish(output, err)
func (t *Tracer) StartToolCall(ctx context.Context, toolName, input string) (context.Context, func(output string, err error)) {
	if t == nil || t.tracer == nil {
		return ctx, func(string, error) {}
	}
	ctx, span := t.tracer.Start(ctx, "routex.tool.execute",
		trace.WithSpanKind(trace.SpanKindInternal),
	)
	span.SetAttributes(
		attribute.String("routex.tool.name", toolName),
		attribute.Int("routex.tool.input_len", len(input)),
	)
	return ctx, func(output string, err error) {
		span.SetAttributes(attribute.Int("routex.tool.output_len", len(output)))
		setSpanError(span, err)
		span.End()
	}
}

// setSpanError marks a span as errored if err is non-nil.
func setSpanError(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	} else {
		span.SetStatus(codes.Ok, "")
	}
}

// serviceVersion returns the CLI version if available, else "unknown".
func serviceVersion() string {
	return "dev"
}
