package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Ad3bay0c/routex/tools"
)

// HTTPRequestTool makes HTTP requests to any REST API endpoint.
//
// Supports four authentication patterns — configure in agents.yaml:
//
//  1. Query string key  (OpenWeatherMap, Google Maps, etc.)
//     extra:
//     query_api_key:      "env:OWM_API_KEY"
//     query_api_key_name: "appid"          # becomes ?appid=KEY
//
//  2. Bearer token  (GitHub, most modern REST APIs)
//     extra:
//     bearer_token: "env:GITHUB_TOKEN"    # Authorization: Bearer TOKEN
//
//  3. Header key  (SendGrid, Stripe, etc.)
//     api_key: "env:MY_KEY"                 # X-Api-Key: KEY
//
//  4. Custom header  (any header name)
//     extra:
//     header_Authorization: "Bearer env:MY_KEY"
//     header_X-Custom:      "value"
//
// Per-call query parameters:
//
//	The LLM can pass "params": {"q": "Lagos,NG", "units": "metric"}
//	in the tool input — these are appended to the URL as query string.
//	Default auth params (query_api_key) are merged automatically.
type HTTPRequestTool struct {
	client          *http.Client
	defaultHeaders  map[string]string
	defaultParams   map[string]string // default query params added to every request
	apiKey          string            // sent as X-Api-Key header
	bearerToken     string            // sent as Authorization: Bearer
	queryAPIKey     string            // sent as a query string param
	queryAPIKeyName string            // the param name, e.g. "appid", "key", "api_key"
	maxBodyBytes    int
}

type httpRequestInput struct {
	// Method is the HTTP verb. Supported: GET, POST, PUT, PATCH, DELETE, HEAD.
	// Defaults to GET.
	Method string `json:"method,omitempty"`

	// URL is the full endpoint. Must start with http:// or https://.
	// Do NOT include auth params in the URL — use params{} or the
	// tool's configured auth instead. Keeping auth out of URLs avoids
	// it appearing in logs.
	URL string `json:"url"`

	// Params are query string parameters appended to the URL.
	// Merged with default params from agents.yaml — per-call wins on conflict.
	// Use this for API parameters like {"q": "Lagos,NG", "units": "metric"}.
	Params map[string]string `json:"params,omitempty"`

	// Headers are additional headers for this specific request.
	// Merged with default headers from agents.yaml — per-call wins.
	Headers map[string]string `json:"headers,omitempty"`

	// Body is the request body. Typically JSON for POST/PUT/PATCH.
	Body string `json:"body,omitempty"`

	// APIKey overrides the default api_key for this call. Sent as X-Api-Key.
	APIKey string `json:"api_key,omitempty"`

	// BearerToken overrides the default bearer_token for this call.
	BearerToken string `json:"bearer_token,omitempty"`

	// MaxChars limits response body characters returned. Default: 8000.
	MaxChars int `json:"max_chars,omitempty"`
}

// HTTPResponse is returned to the LLM after every call.
type HTTPResponse struct {
	// StatusCode is the HTTP status (200, 404, 500, etc.)
	StatusCode int `json:"status_code"`

	// StatusText is the human-readable status ("200 OK", "404 Not Found")
	StatusText string `json:"status_text"`

	// Success is true when StatusCode is 2xx.
	Success bool `json:"success"`

	// Headers contains selected useful response headers.
	Headers map[string]string `json:"headers"`

	// Body is the response body as a string, truncated to MaxChars.
	// JSON responses are pretty-printed for readability.
	Body string `json:"body"`

	// BodyJSON holds the parsed body when Content-Type is JSON.
	// The LLM can navigate this directly: result.body_json.main.temp
	BodyJSON any `json:"body_json,omitempty"`

	// Truncated is true when the body was cut at MaxChars.
	Truncated bool `json:"truncated"`

	// Error describes any network-level error (timeout, DNS failure, etc.)
	// Empty when the request completed, even if the status code is an error.
	Error string `json:"error,omitempty"`

	// Method and URL are echoed back for clarity in multi-step pipelines.
	Method string `json:"method"`
	URL    string `json:"url"`
}

// HTTPRequest returns a ready-to-use HTTPRequestTool with no defaults.
func HTTPRequest() *HTTPRequestTool {
	return &HTTPRequestTool{
		client:         &http.Client{Timeout: 20 * time.Second},
		defaultHeaders: make(map[string]string),
		defaultParams:  make(map[string]string),
		maxBodyBytes:   512 * 1024,
	}
}

func (t *HTTPRequestTool) Name() string { return "http_request" }

func (t *HTTPRequestTool) Schema() tools.Schema {
	return tools.Schema{
		Description: "Make an HTTP request to any REST API endpoint. " +
			"Supports GET, POST, PUT, PATCH, DELETE. " +
			"Pass query parameters via 'params' — they are appended to the URL automatically. " +
			"Authentication (API key, Bearer token, query string key) is configured in agents.yaml " +
			"and applied automatically — you do not need to add auth to every call. " +
			"Returns status code, headers, and parsed body.",
		Parameters: map[string]tools.Parameter{
			"url": {
				Type: "string",
				Description: "Full URL of the endpoint. Must start with http:// or https://. " +
					"Do not include auth params — those are handled automatically.",
				Required: true,
			},
			"method": {
				Type:        "string",
				Description: "HTTP method: GET (default), POST, PUT, PATCH, DELETE, HEAD.",
				Required:    false,
			},
			"params": {
				Type: "object",
				Description: "Query string parameters appended to the URL. " +
					"Example: {\"q\": \"Lagos,NG\", \"units\": \"metric\"}. " +
					"Auth params configured in agents.yaml are added automatically.",
				Required: false,
			},
			"body": {
				Type:        "string",
				Description: "Request body. For JSON APIs pass a JSON string: '{\"key\":\"value\"}'",
				Required:    false,
			},
			"headers": {
				Type:        "object",
				Description: "Additional headers for this request. Overrides defaults from agents.yaml.",
				Required:    false,
			},
			"api_key": {
				Type:        "string",
				Description: "Override the default api_key for this call. Sent as X-Api-Key header.",
				Required:    false,
			},
			"bearer_token": {
				Type:        "string",
				Description: "Override the default bearer_token for this call.",
				Required:    false,
			},
			"max_chars": {
				Type:        "number",
				Description: "Maximum characters of response body to return. Default: 8000.",
				Required:    false,
			},
		},
	}
}

func (t *HTTPRequestTool) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	// — parse input
	var params httpRequestInput
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("http_request: invalid input: %w", err)
	}
	if strings.TrimSpace(params.URL) == "" {
		return nil, fmt.Errorf("http_request: url is required")
	}

	method := strings.ToUpper(params.Method)
	if method == "" {
		method = http.MethodGet
	}
	validMethods := map[string]bool{
		http.MethodGet: true, http.MethodPost: true,
		http.MethodPut: true, http.MethodPatch: true,
		http.MethodDelete: true, http.MethodHead: true,
	}
	if !validMethods[method] {
		return nil, fmt.Errorf(
			"http_request: unsupported method %q — valid: GET, POST, PUT, PATCH, DELETE, HEAD",
			method,
		)
	}

	maxChars := params.MaxChars
	if maxChars <= 0 {
		maxChars = 8000
	}

	// — build the final URL with query parameters
	// Priority: default params → query auth key → per-call params (highest)
	finalURL, err := t.buildURL(params.URL, params.Params)
	if err != nil {
		return nil, fmt.Errorf("http_request: build url: %w", err)
	}

	// — build the request
	var bodyReader io.Reader
	if params.Body != "" {
		bodyReader = strings.NewReader(params.Body)
	}

	req, err := http.NewRequestWithContext(ctx, method, finalURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("http_request: build request: %w", err)
	}

	// — apply headers in priority order:
	//   lowest  → default headers from agents.yaml
	//   middle  → auth headers (api_key, bearer_token)
	//   highest → per-call headers from LLM input

	// Layer 1 - Default headers
	for k, v := range t.defaultHeaders {
		req.Header.Set(k, v)
	}

	// Layer 2 - Header-based auth — per-call overrides yaml default
	apiKey := params.APIKey
	if apiKey == "" {
		apiKey = t.apiKey
	}
	if apiKey != "" {
		req.Header.Set("X-Api-Key", apiKey)
		req.Header.Set("API-KEY", apiKey)
		req.Header.Set("API_KEY", apiKey)
	}

	bearerToken := params.BearerToken
	if bearerToken == "" {
		bearerToken = t.bearerToken
	}
	if bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	}

	// Layer 3 - Per-call headers from LLM (highest priority)
	for k, v := range params.Headers {
		req.Header.Set(k, v)
	}

	// Auto-set Content-Type for requests with a body
	if params.Body != "" && req.Header.Get("Content-Type") == "" {
		if isJSON(params.Body) {
			req.Header.Set("Content-Type", "application/json")
		} else {
			req.Header.Set("Content-Type", "text/plain")
		}
	}

	// — execute
	resp, err := t.client.Do(req)
	if err != nil {
		// Network-level error — return structured response so LLM can reason about it
		return json.Marshal(HTTPResponse{
			Method: method,
			URL:    finalURL,
			Error:  err.Error(),
		})
	}
	defer resp.Body.Close() //nolint:errcheck

	// — read body
	rawBody, err := io.ReadAll(io.LimitReader(resp.Body, int64(t.maxBodyBytes)))
	if err != nil {
		return nil, fmt.Errorf("http_request: read response body: %w", err)
	}

	// — build response
	result := HTTPResponse{
		StatusCode: resp.StatusCode,
		StatusText: resp.Status,
		Success:    resp.StatusCode >= 200 && resp.StatusCode < 300,
		Method:     method,
		URL:        finalURL,
		Headers:    extractUsefulHeaders(resp.Header),
	}

	bodyStr := string(rawBody)
	contentType := resp.Header.Get("Content-Type")

	if strings.Contains(contentType, "application/json") || isJSON(bodyStr) {
		var parsed any
		if err := json.Unmarshal(rawBody, &parsed); err == nil {
			result.BodyJSON = parsed
			if pretty, err := json.MarshalIndent(parsed, "", "  "); err == nil {
				bodyStr = string(pretty)
			}
		}
	}

	if utf8.RuneCountInString(bodyStr) > maxChars {
		runes := []rune(bodyStr)
		bodyStr = string(runes[:maxChars])
		result.Truncated = true
	}
	result.Body = bodyStr

	return json.Marshal(result)
}

// buildURL merges the base URL with default params, auth query params,
// and per-call params into a final URL.
//
// Priority (lowest to highest):
//  1. defaultParams from agents.yaml extra: (e.g. units=metric)
//  2. queryAPIKey from agents.yaml (e.g. appid=KEY)
//  3. per-call params from LLM input (highest — can override defaults)
func (t *HTTPRequestTool) buildURL(rawURL string, callParams map[string]string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("invalid url %q: %w", rawURL, err)
	}

	// Start with any params already in the URL
	q := parsed.Query()

	// Layer 1 — default params from agents.yaml extra: (prefix "param_")
	for k, v := range t.defaultParams {
		q.Set(k, v)
	}

	// Layer 2 — query string auth key (e.g. appid for OpenWeatherMap)
	// Added after defaultParams so it is not accidentally overridden by them
	if t.queryAPIKey != "" && t.queryAPIKeyName != "" {
		q.Set(t.queryAPIKeyName, t.queryAPIKey)
	}

	// Layer 3 — per-call params from LLM (highest priority)
	for k, v := range callParams {
		q.Set(k, v)
	}

	parsed.RawQuery = q.Encode()
	return parsed.String(), nil
}

func extractUsefulHeaders(h http.Header) map[string]string {
	useful := []string{
		"Content-Type", "Content-Length", "Location",
		"X-Request-Id", "X-RateLimit-Limit", "X-RateLimit-Remaining",
		"X-RateLimit-Reset", "Retry-After", "ETag", "Last-Modified", "Link",
	}
	result := make(map[string]string, len(useful))
	for _, key := range useful {
		if val := h.Get(key); val != "" {
			result[key] = val
		}
	}
	return result
}

func isJSON(s string) bool {
	s = strings.TrimSpace(s)
	return (strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}")) ||
		(strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]"))
}

func init() {
	tools.RegisterBuiltin("http_request", func(cfg tools.ToolConfig) (tools.Tool, error) {
		t := HTTPRequest()

		// Header-based auth
		t.apiKey = cfg.APIKey
		t.bearerToken = cfg.Extra["bearer_token"]

		// Query string auth (OpenWeatherMap, Google Maps, etc.)
		// extra:
		//   query_api_key:      "env:OWM_API_KEY"
		//   query_api_key_name: "appid"
		t.queryAPIKey = cfg.Extra["query_api_key"]
		t.queryAPIKeyName = cfg.Extra["query_api_key_name"]
		if t.queryAPIKeyName == "" && t.queryAPIKey != "" {
			// Sensible default if name not specified
			t.queryAPIKeyName = "api_key"
		}

		for k, v := range cfg.Extra {
			switch {
			case strings.HasPrefix(k, "header_"):
				// extra:
				//   header_Accept:           "application/json"
				//   header_X-GitHub-Api-Version: "2022-11-28"
				headerName := strings.TrimPrefix(k, "header_")
				headerName = strings.ReplaceAll(headerName, "_", "-")
				t.defaultHeaders[headerName] = v

			case strings.HasPrefix(k, "param_"):
				// extra:
				//   param_units: "metric"
				//   param_lang:  "en"
				paramName := strings.TrimPrefix(k, "param_")
				t.defaultParams[paramName] = v
			case strings.HasPrefix(k, "query_"):
				// extra:
				//   query_app_id:      "env:OWM_API_KEY"
				//   query_language: "en"
				queryName := strings.TrimPrefix(k, "query_")
				// it's already handled above
				if queryName != "api_key" && queryName != "api_key_name" {
					t.defaultParams[queryName] = v
				}
			}
		}

		return t, nil
	})
}

var _ tools.Tool = (*HTTPRequestTool)(nil)
