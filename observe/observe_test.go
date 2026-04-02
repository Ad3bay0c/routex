package observe

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMetrics_NilReceiverSafe(t *testing.T) {
	var m *Metrics
	m.RecordTokens("a", "openai", 10)
	m.RecordToolCall("t", time.Millisecond, nil)
	m.RecordToolCall("t", time.Millisecond, io.EOF)
	m.RecordAgentRun("id", "role", time.Second)
	m.RecordAgentFailure("id")
	m.RecordRun("crew", 2*time.Second)
	if err := m.Shutdown(context.Background()); err != nil {
		t.Errorf("Shutdown: %v", err)
	}
}

func TestNewMetrics_HealthzAndMetrics(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	m, err := NewMetrics("test-svc", addr)
	if err != nil {
		t.Fatalf("NewMetrics: %v", err)
	}
	t.Cleanup(func() {
		_ = m.Shutdown(context.Background())
	})

	time.Sleep(150 * time.Millisecond)
	resp, err := http.Get("http://" + addr + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz status = %d", resp.StatusCode)
	}

	m.RecordTokens("agent-1", "openai", 7)

	resp, err = http.Get("http://" + addr + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !strings.Contains(string(body), "routex_tokens_total") {
		t.Errorf("metrics body should contain routex_tokens_total, got: %s", string(body))
	}
}

func TestNewTracer_EmptyEndpoint(t *testing.T) {
	_, err := NewTracer(context.Background(), "svc", "")
	if err == nil {
		t.Fatal("expected error for empty endpoint")
	}
	if !strings.Contains(err.Error(), "endpoint") {
		t.Errorf("error should mention endpoint: %v", err)
	}
}

func TestNewNoopTracer_StartRunAndAgent(t *testing.T) {
	tr := NewNoopTracer()
	ctx := context.Background()
	ctx, finishRun := tr.StartRun(ctx, "run-1", "task")
	finishRun(nil)
	ctx, finishAgent := tr.StartAgent(ctx, "a1", "worker")
	finishAgent(nil)
	_, finishLLM := tr.StartLLMCall(ctx, "openai", "gpt-4o")
	finishLLM(0, nil)
	_, finishTool := tr.StartToolCall(ctx, "grep", "{}")
	finishTool("out", nil)
	if err := tr.Shutdown(ctx); err != nil {
		t.Errorf("Shutdown noop: %v", err)
	}
}

func TestTracer_NilReceiverSafe(t *testing.T) {
	var tr *Tracer
	ctx := context.Background()
	ctx, finishRun := tr.StartRun(ctx, "r", "in")
	finishRun(nil)
	ctx, finishAgent := tr.StartAgent(ctx, "a", "r")
	finishAgent(nil)
	_, finishLLM := tr.StartLLMCall(ctx, "p", "m")
	finishLLM(0, nil)
	_, finishTool := tr.StartToolCall(ctx, "t", "i")
	finishTool("", nil)
	if err := tr.Shutdown(ctx); err != nil {
		t.Errorf("Shutdown: %v", err)
	}
}

func TestNewTracer_OTLPEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/traces" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tr, err := NewTracer(ctx, "routex-test", srv.URL+"/v1/traces")
	if err != nil {
		t.Fatalf("NewTracer: %v", err)
	}
	t.Cleanup(func() {
		_ = tr.Shutdown(context.Background())
	})

	ctx2, finish := tr.StartRun(ctx, "run-x", "hello")
	finish(nil)
	_ = ctx2
}
