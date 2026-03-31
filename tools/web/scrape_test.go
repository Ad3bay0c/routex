package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Ad3bay0c/routex/tools"
)

func TestScrapeTool_NameAndSchema(t *testing.T) {
	tool := Scrape("k")
	if tool.Name() != "scrape" {
		t.Errorf("Name = %q", tool.Name())
	}
	s := tool.Schema()
	if s.Parameters["url"].Required != true {
		t.Error("url should be required")
	}
}

func TestScrapeTool_Execute_SuccessStripsHTML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s", r.Method)
		}
		q := r.URL.Query()
		if q.Get("api_key") != "test-key" {
			t.Errorf("api_key = %q", q.Get("api_key"))
		}
		if q.Get("render_js") != "true" {
			t.Errorf("render_js = %q", q.Get("render_js"))
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<html><body><p>Hello Scrape</p></body></html>"))
	}))
	t.Cleanup(srv.Close)

	tool := Scrape("test-key")
	tool.baseURL = srv.URL + "/"
	tool.client = srv.Client()

	out, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"url":       "https://example.com/page",
		"render_js": true,
	}))
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	mustUnmarshal(t, out, &parsed)
	content := parsed["content"].(string)
	if strings.Contains(content, "<p>") {
		t.Errorf("expected stripped HTML, got %q", content)
	}
	if !strings.Contains(content, "Hello Scrape") {
		t.Errorf("content = %q", content)
	}
}

func TestScrapeTool_RenderJSFalse(t *testing.T) {
	var gotRender string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRender = r.URL.Query().Get("render_js")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<html><body>x</body></html>"))
	}))
	t.Cleanup(srv.Close)

	tool := Scrape("k")
	tool.baseURL = srv.URL + "/"
	tool.client = srv.Client()

	_, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"url":       "https://a.test/",
		"render_js": false,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if gotRender != "false" {
		t.Errorf("render_js = %q", gotRender)
	}
}

func TestScrapeTool_MaxCharsTruncation(t *testing.T) {
	longHTML := "<html><body><p>" + strings.Repeat("x", 500) + "</p></body></html>"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(longHTML))
	}))
	t.Cleanup(srv.Close)

	tool := Scrape("k")
	tool.baseURL = srv.URL + "/"
	tool.client = srv.Client()

	out, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"url":       "https://b.test/",
		"max_chars": 50,
	}))
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	mustUnmarshal(t, out, &parsed)
	if parsed["truncated"] != true {
		t.Errorf("truncated = %v", parsed["truncated"])
	}
	if int(parsed["char_count"].(float64)) > 50 {
		t.Errorf("char_count = %v", parsed["char_count"])
	}
}

func TestScrapeTool_Errors(t *testing.T) {
	tool := Scrape("k")

	_, err := tool.Execute(context.Background(), []byte(`not json`))
	if err == nil || !strings.Contains(err.Error(), "invalid input") {
		t.Fatalf("want invalid input: %v", err)
	}

	_, err = tool.Execute(context.Background(), mustMarshal(t, map[string]any{"url": ""}))
	if err == nil || !strings.Contains(err.Error(), "url is required") {
		t.Fatalf("want url required: %v", err)
	}
}

func TestScrapeTool_HTTPStatusErrors(t *testing.T) {
	tests := []struct {
		code int
		want string
	}{
		{http.StatusForbidden, "invalid API key"},
		{http.StatusTooManyRequests, "rate limit"},
		{http.StatusPaymentRequired, "credits exhausted"},
		{http.StatusBadGateway, "api returned status"},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprint(tt.code), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.code)
			}))
			t.Cleanup(srv.Close)

			tool := Scrape("k")
			tool.baseURL = srv.URL + "/"
			tool.client = srv.Client()

			_, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
				"url": "https://example.com/",
			}))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("code %d: err = %v, want %q", tt.code, err, tt.want)
			}
		})
	}
}

func TestScrapeTool_OKWithNonJSONBodyStillReturnsContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`not-json`))
	}))
	t.Cleanup(srv.Close)

	tool := Scrape("k")
	tool.baseURL = srv.URL + "/"
	tool.client = srv.Client()

	out, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"url": "https://example.com/",
	}))
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	mustUnmarshal(t, out, &parsed)
	// Message ID empty but success payload still returned
	if parsed["content"] == nil {
		t.Fatalf("missing content: %v", parsed)
	}
}

func TestScrapeTool_RequestFailed(t *testing.T) {
	tool := Scrape("k")
	tool.baseURL = "http://127.0.0.1:1/"
	tool.client = &http.Client{}

	_, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"url": "https://example.com/",
	}))
	if err == nil || !strings.Contains(err.Error(), "request failed") {
		t.Fatalf("err = %v", err)
	}
}

func TestScrapeBuiltinRegistration(t *testing.T) {
	_, err := tools.Resolve("scrape", tools.ToolConfig{Name: "scrape"})
	if err == nil || !strings.Contains(err.Error(), "api_key") {
		t.Fatalf("err = %v", err)
	}

	tool, err := tools.Resolve("scrape", tools.ToolConfig{Name: "scrape", APIKey: "bee-key"})
	if err != nil {
		t.Fatal(err)
	}
	st, ok := tool.(*ScrapeTool)
	if !ok {
		t.Fatalf("type %T", tool)
	}
	if st.apiKey != "bee-key" {
		t.Errorf("apiKey = %q", st.apiKey)
	}
}
