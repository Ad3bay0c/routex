package web

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPRequest_GetSuccess(t *testing.T) {
	srv := newTestServer(t, 200, `{"status":"ok"}`, "application/json")

	tool := HTTPRequest()
	result, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"url":    srv.URL + "/test",
		"method": "GET",
	}))
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	var resp HTTPResponse
	mustUnmarshal(t, result, &resp)

	if resp.StatusCode != 200 {
		t.Errorf("StatusCode = %d, want 200", resp.StatusCode)
	}
	if !resp.Success {
		t.Error("Success = false, want true")
	}
	if resp.Method != "GET" {
		t.Errorf("Method = %q, want GET", resp.Method)
	}
	// JSON body should be parsed into BodyJSON
	if resp.BodyJSON == nil {
		t.Error("BodyJSON should be non-nil for JSON response")
	}
}

func TestHTTPRequest_PostWithBody(t *testing.T) {
	var capturedBody string
	var capturedContentType string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedContentType = r.Header.Get("Content-Type")
		buf := new(strings.Builder)
		io.Copy(buf, r.Body)
		capturedBody = buf.String()
		w.WriteHeader(201)
		w.Write([]byte(`{"created":true}`))
	}))
	t.Cleanup(srv.Close)

	tool := HTTPRequest()
	_, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"url":    srv.URL,
		"method": "POST",
		"body":   `{"name":"routex"}`,
	}))
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	if capturedBody != `{"name":"routex"}` {
		t.Errorf("body = %q, want %q", capturedBody, `{"name":"routex"}`)
	}
	if !strings.Contains(capturedContentType, "application/json") {
		t.Errorf("Content-Type = %q, should contain application/json", capturedContentType)
	}
}

func TestHTTPRequest_QueryParams(t *testing.T) {
	var capturedQuery string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.RawQuery
		w.WriteHeader(200)
		w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	tool := HTTPRequest()
	_, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"url":    srv.URL,
		"method": "GET",
		"params": map[string]string{"q": "Lagos,NG", "units": "metric"},
	}))
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	if !strings.Contains(capturedQuery, "q=Lagos") {
		t.Errorf("query %q should contain q=Lagos", capturedQuery)
	}
	if !strings.Contains(capturedQuery, "units=metric") {
		t.Errorf("query %q should contain units=metric", capturedQuery)
	}
}

func TestHTTPRequest_QueryAPIKey(t *testing.T) {
	var capturedQuery string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.RawQuery
		w.WriteHeader(200)
		w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	// Configure query string auth (like OpenWeatherMap's ?appid=KEY)
	tool := HTTPRequest()
	tool.queryAPIKey = "test-api-key"
	tool.queryAPIKeyName = "appid"

	_, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"url":    srv.URL,
		"params": map[string]string{"q": "Lagos,NG"},
	}))
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	if !strings.Contains(capturedQuery, "appid=test-api-key") {
		t.Errorf("query %q should contain appid=test-api-key", capturedQuery)
	}
}

func TestHTTPRequest_DefaultParams(t *testing.T) {
	var capturedQuery string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.RawQuery
		w.WriteHeader(200)
		w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	tool := HTTPRequest()
	tool.defaultParams["units"] = "metric"
	tool.defaultParams["lang"] = "en"

	_, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"url": srv.URL,
	}))
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	if !strings.Contains(capturedQuery, "units=metric") {
		t.Errorf("query should contain default param units=metric, got %q", capturedQuery)
	}
}

func TestHTTPRequest_PerCallParamsOverrideDefaults(t *testing.T) {
	var capturedQuery string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.RawQuery
		w.WriteHeader(200)
		w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	tool := HTTPRequest()
	tool.defaultParams["units"] = "metric" // default

	_, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"url":    srv.URL,
		"params": map[string]string{"units": "imperial"}, // override
	}))
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	if !strings.Contains(capturedQuery, "units=imperial") {
		t.Errorf("per-call param should override default, query = %q", capturedQuery)
	}
}

func TestHTTPRequest_BearerTokenHeader(t *testing.T) {
	var capturedAuth string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		w.WriteHeader(200)
		w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	tool := HTTPRequest()
	tool.bearerToken = "my-secret-token"

	_, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"url": srv.URL,
	}))
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	if capturedAuth != "Bearer my-secret-token" {
		t.Errorf("Authorization = %q, want %q", capturedAuth, "Bearer my-secret-token")
	}
}

func TestHTTPRequest_CustomHeaders(t *testing.T) {
	var capturedCustom string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedCustom = r.Header.Get("X-Custom-Header")
		w.WriteHeader(200)
		w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	tool := HTTPRequest()
	_, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"url":     srv.URL,
		"headers": map[string]string{"X-Custom-Header": "my-value"},
	}))
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	if capturedCustom != "my-value" {
		t.Errorf("X-Custom-Header = %q, want %q", capturedCustom, "my-value")
	}
}

func TestHTTPRequest_ErrorStatusReturnedInResponse(t *testing.T) {
	srv := newTestServer(t, 404, `{"error":"not found"}`, "application/json")

	tool := HTTPRequest()
	result, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"url": srv.URL,
	}))
	// 404 is NOT a Go error — it is a valid HTTP response returned in the struct
	if err != nil {
		t.Fatalf("Execute() should not error for 404, got: %v", err)
	}

	var resp HTTPResponse
	mustUnmarshal(t, result, &resp)

	if resp.StatusCode != 404 {
		t.Errorf("StatusCode = %d, want 404", resp.StatusCode)
	}
	if resp.Success {
		t.Error("Success = true for 404, want false")
	}
}

func TestHTTPRequest_NetworkErrorReturnedInResponse(t *testing.T) {
	tool := HTTPRequest()
	result, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"url": "http://127.0.0.1:1", // nothing listening here
	}))
	// Network errors come back in the response Error field, not as Go errors
	if err != nil {
		t.Fatalf("Execute() should not return Go error for network failure")
	}

	var resp HTTPResponse
	mustUnmarshal(t, result, &resp)

	if resp.Error == "" {
		t.Error("Error field should be non-empty for network failure")
	}
}

func TestHTTPRequest_InvalidMethod(t *testing.T) {
	tool := HTTPRequest()
	_, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"url":    "http://example.com",
		"method": "HACK",
	}))
	if err == nil {
		t.Fatal("should error for unsupported HTTP method")
	}
	if err.Error() != "http_request: unsupported method \"HACK\" — valid: GET, POST, PUT, PATCH, DELETE, HEAD" {
		t.Errorf("invalid HTTP method error: %v", err)
	}
}

func TestHTTPRequest_MissingURL(t *testing.T) {
	tool := HTTPRequest()
	_, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{}))
	if err == nil {
		t.Fatal("should error when url is missing")
	}

	if err.Error() != "http_request: url is required" {
		t.Errorf("error = %q, want %q", err, "http_request: url is required")
	}
}

func TestHTTPRequest_ResponseBodyTruncation(t *testing.T) {
	bigBody := strings.Repeat("x", 10000)
	srv := newTestServer(t, 200, bigBody, "text/plain")

	tool := HTTPRequest()
	result, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"url":       srv.URL,
		"max_chars": 100,
	}))
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	var resp HTTPResponse
	mustUnmarshal(t, result, &resp)

	if !resp.Truncated {
		t.Error("Truncated = false, want true")
	}
	if len(resp.Body) > 100 {
		t.Errorf("Body len = %d, want <= 100", len(resp.Body))
	}
}

func TestHTTPRequest_NameAndSchema(t *testing.T) {
	tool := HTTPRequest()
	if tool.Name() != "http_request" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "http_request")
	}
	schema := tool.Schema()
	if schema.Description == "" {
		t.Error("Schema.Description should not be empty")
	}
	required := []string{"url"}
	for _, p := range required {
		if param, ok := schema.Parameters[p]; !ok {
			t.Errorf("schema missing required parameter %q", p)
		} else if !param.Required {
			t.Errorf("parameter %q should be Required=true", p)
		}
	}
}
