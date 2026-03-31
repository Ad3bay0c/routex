package observe

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics holds all Prometheus counters and histograms for a runtime.
// It is safe to call all methods on a nil *Metrics — they become no-ops.
type Metrics struct {
	registry *prometheus.Registry

	// Token usage per agent and provider
	tokensTotal *prometheus.CounterVec

	// Tool call counts per tool name and status (success/error)
	toolCallsTotal *prometheus.CounterVec

	// Tool execution duration histogram
	toolDuration *prometheus.HistogramVec

	// Agent run duration histogram
	agentDuration *prometheus.HistogramVec

	// Crew run duration histogram
	runDuration *prometheus.HistogramVec

	// Agent failures per agent ID and restart policy
	agentFailuresTotal *prometheus.CounterVec

	// HTTP server for /metrics endpoint
	server *http.Server
}

// NewMetrics creates a Metrics instance and registers all metrics.
// addr is the address to serve /metrics on, e.g. ":9090".
// Call Shutdown() when the runtime stops.
func NewMetrics(serviceName, addr string) (*Metrics, error) {
	reg := prometheus.NewRegistry()

	// Standard Go runtime and process metrics
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	namespace := "routex"

	m := &Metrics{
		registry: reg,

		tokensTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "tokens_total",
				Help:      "Total LLM tokens consumed, partitioned by agent ID and provider.",
			},
			[]string{"agent_id", "provider"},
		),

		toolCallsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "tool_calls_total",
				Help:      "Total tool executions, partitioned by tool name and status.",
			},
			[]string{"tool_name", "status"}, // status: success | error
		),

		toolDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Name:      "tool_duration_seconds",
				Help:      "Tool execution latency in seconds.",
				Buckets:   prometheus.DefBuckets,
			},
			[]string{"tool_name"},
		),

		agentDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Name:      "agent_duration_seconds",
				Help:      "Total agent run duration in seconds.",
				Buckets:   []float64{1, 5, 10, 30, 60, 120, 300},
			},
			[]string{"agent_id", "role"},
		),

		runDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Name:      "run_duration_seconds",
				Help:      "Total crew run duration in seconds.",
				Buckets:   []float64{5, 15, 30, 60, 120, 300, 600},
			},
			[]string{"runtime_name"},
		),

		agentFailuresTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "agent_failures_total",
				Help:      "Total agent failures before giving up.",
			},
			[]string{"agent_id"},
		),
	}

	// Register all metrics
	for _, c := range []prometheus.Collector{
		m.tokensTotal,
		m.toolCallsTotal,
		m.toolDuration,
		m.agentDuration,
		m.runDuration,
		m.agentFailuresTotal,
	} {
		if err := reg.Register(c); err != nil {
			return nil, fmt.Errorf("observe: register metric: %w", err)
		}
	}

	// Start the /metrics HTTP server
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{
		EnableOpenMetrics: true,
	}))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok")) //nolint:errcheck
	})

	m.server = &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	go func() {
		if err := m.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			// Log but don't crash — metrics failing should not stop the crew
			fmt.Printf("observe: metrics server error: %v\n", err)
		}
	}()

	return m, nil
}

// NewNoopMetrics returns a Metrics that does nothing.
// Used when metrics are disabled in config
func NewNoopMetrics() *Metrics {
	return nil
}

// Shutdown stops the metrics HTTP server.
func (m *Metrics) Shutdown(ctx context.Context) error {
	if m == nil || m.server == nil {
		return nil
	}
	return m.server.Shutdown(ctx)
}

// RecordTokens records LLM token usage for an agent.
func (m *Metrics) RecordTokens(agentID, provider string, count int) {
	if m == nil {
		return
	}
	m.tokensTotal.WithLabelValues(agentID, provider).Add(float64(count))
}

// RecordToolCall records a tool execution with its status and duration.
func (m *Metrics) RecordToolCall(toolName string, duration time.Duration, err error) {
	if m == nil {
		return
	}
	status := "success"
	if err != nil {
		status = "error"
	}
	m.toolCallsTotal.WithLabelValues(toolName, status).Inc()
	m.toolDuration.WithLabelValues(toolName).Observe(duration.Seconds())
}

// RecordAgentRun records a completed agent run with its duration.
func (m *Metrics) RecordAgentRun(agentID, role string, duration time.Duration) {
	if m == nil {
		return
	}
	m.agentDuration.WithLabelValues(agentID, role).Observe(duration.Seconds())
}

// RecordAgentFailure increments the failure counter for an agent.
func (m *Metrics) RecordAgentFailure(agentID string) {
	if m == nil {
		return
	}
	m.agentFailuresTotal.WithLabelValues(agentID).Inc()
}

// RecordRun records a completed crew run with its duration.
func (m *Metrics) RecordRun(runtimeName string, duration time.Duration) {
	if m == nil {
		return
	}
	m.runDuration.WithLabelValues(runtimeName).Observe(duration.Seconds())
}
