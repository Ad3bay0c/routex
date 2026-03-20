package search

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/Ad3bay0c/routex/tools"
)

// BraveSearchTool searches the web using the Brave Search API.
//
// Brave returns cleaner, more structured results than DuckDuckGo
// and is the recommended search tool for production agent workloads.
// It returns result age so agents can prioritise recent content.
//
// API key: https://api.search.brave.com (free tier: 2,000 queries/month)
//
// agents.yaml:
//
//	tools:
//	  - name: "brave_search"
//	    api_key: "env:BRAVE_API_KEY"
//	    max_results: 5
type BraveSearchTool struct {
	client     *http.Client
	apiKey     string
	maxResults int
}

// braveSearchInput is the JSON the LLM sends when calling this tool.
type braveSearchInput struct {
	Query      string `json:"query"`
	MaxResults int    `json:"max_results,omitempty"`
}

// braveSearchResult is one item in the results list returned to the LLM.
type braveSearchResult struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Description string `json:"description"`
	Age         string `json:"age,omitempty"` // e.g. "2 days ago"
}

// braveSearchOutput is the full response sent back to the LLM.
type braveSearchOutput struct {
	Query   string              `json:"query"`
	Results []braveSearchResult `json:"results"`
	Total   int                 `json:"total"`
}

// braveAPIResponse mirrors the Brave Search API response.
// Only the fields we actually use are mapped.
type braveAPIResponse struct {
	Web struct {
		Results []struct {
			Title       string `json:"title"`
			URL         string `json:"url"`
			Description string `json:"description"`
			Age         string `json:"age"`
		} `json:"results"`
	} `json:"web"`
}

// BraveSearch returns a ready-to-use BraveSearchTool.
// Called directly in programmatic mode: rt.RegisterTool(tools.BraveSearch(apiKey))
// In YAML mode the init() function below handles construction automatically.
func BraveSearch(apiKey string) *BraveSearchTool {
	return &BraveSearchTool{
		client:     &http.Client{Timeout: 15 * time.Second},
		apiKey:     apiKey,
		maxResults: 5,
	}
}

// Name returns the tool identifier agents use in their tools: list.
// This satisfies the Tool interface.
func (t *BraveSearchTool) Name() string {
	return "brave_search"
}

// Schema describes this tool to the LLM so it knows when and how to call it.
// This satisfies the Tool interface.
func (t *BraveSearchTool) Schema() tools.Schema {
	return tools.Schema{
		Description: "Search the web using Brave Search for accurate, up-to-date results. " +
			"Prefer this over web_search for production workloads — results are cleaner and " +
			"include publication age so you can prioritise recent content. " +
			"Use for current events, research, fact-checking, and finding sources.",
		Parameters: map[string]tools.Parameter{
			"query": {
				Type:        "string",
				Description: "The search query. Be specific for best results. Example: 'Go 1.24 release notes features'",
				Required:    true,
			},
			"max_results": {
				Type:        "number",
				Description: "Number of results to return (1-10). Defaults to 5.",
				Required:    false,
			},
		},
	}
}

// Execute runs the Brave search when the LLM requests it.
// This satisfies the Tool interface.
func (t *BraveSearchTool) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	// — parse the LLM's input
	var params braveSearchInput
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("brave_search: invalid input: %w", err)
	}
	if params.Query == "" {
		return nil, fmt.Errorf("brave_search: query is required")
	}
	if params.MaxResults <= 0 || params.MaxResults > 10 {
		params.MaxResults = t.maxResults
	}

	// — build the Brave Search API URL
	apiURL := fmt.Sprintf(
		"https://api.search.brave.com/res/v1/web/search?q=%s&count=%d",
		url.QueryEscape(params.Query),
		params.MaxResults,
	)

	// — make the request
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("brave_search: build request: %w", err)
	}
	// Brave requires these two headers on every request
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", t.apiKey)

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("brave_search: request failed: %w", err)
	}
	defer resp.Body.Close()

	// — handle errors clearly so the LLM knows what went wrong
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return nil, fmt.Errorf("brave_search: invalid API key — check BRAVE_API_KEY")
	case http.StatusTooManyRequests:
		return nil, fmt.Errorf("brave_search: rate limit exceeded — reduce search frequency or upgrade plan")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("brave_search: api returned status %d", resp.StatusCode)
	}

	// — parse the response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("brave_search: read response: %w", err)
	}

	var apiResp braveAPIResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("brave_search: parse response: %w", err)
	}

	// — convert to our clean output format
	results := make([]braveSearchResult, 0, len(apiResp.Web.Results))
	for i, r := range apiResp.Web.Results {
		if i >= params.MaxResults {
			break
		}
		results = append(results, braveSearchResult{
			Title:       r.Title,
			URL:         r.URL,
			Description: r.Description,
			Age:         r.Age,
		})
	}

	return json.Marshal(braveSearchOutput{
		Query:   params.Query,
		Results: results,
		Total:   len(results),
	})
}

// init registers BraveSearchTool as a built-in so the runtime
// auto-instantiates it when "brave_search" appears in agents.yaml.
func init() {
	tools.RegisterBuiltin("brave_search", func(cfg tools.ToolConfig) (tools.Tool, error) {
		if cfg.APIKey == "" {
			return nil, fmt.Errorf(
				"brave_search requires an api_key\n" +
					"  add to agents.yaml:  api_key: \"env:BRAVE_API_KEY\"\n" +
					"  then set the env:    export BRAVE_API_KEY=your-key",
			)
		}
		t := BraveSearch(cfg.APIKey)
		if cfg.MaxResults > 0 {
			t.maxResults = cfg.MaxResults
		}
		return t, nil
	})
}

// compile-time interface check
var _ tools.Tool = (*BraveSearchTool)(nil)
