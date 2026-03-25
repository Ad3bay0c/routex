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

func init() {
	tools.RegisterBuiltin("web_search", func(cfg tools.ToolConfig) (tools.Tool, error) {
		return WebSearch(), nil
	})
}

// WebSearchTool searches the web using the DuckDuckGo Instant Answer API.
// No API key required — DuckDuckGo provides a free JSON endpoint.
// For production use you may want to swap this for a paid search API
// (Google Custom Search, Brave Search, Serper) by implementing the
// same Tool interface with a different Execute() body.
//
// Agents call this tool when they need current information from the web.
// The LLM decides when to call it based on the Schema description.
type WebSearchTool struct {
	// client is reused across calls — creating a new HTTP client
	// on every search would be wasteful and slow
	client  *http.Client
	baseURL string
}

// webSearchInput is the shape of JSON the LLM sends when calling this tool.
// We parse Execute()'s input into this struct.
type webSearchInput struct {
	// Query is the search term — filled in by the LLM based on what it needs
	Query string `json:"query"`

	// MaxResults limits how many results to return.
	// LLM can request 1-10. Defaults to 5 if not set.
	MaxResults int `json:"max_results,omitempty"`
}

// webSearchResult is one search result returned to the LLM.
type webSearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

// webSearchOutput is the full JSON response we send back to the LLM.
type webSearchOutput struct {
	Results []webSearchResult `json:"results"`
	Query   string            `json:"query"`
	Total   int               `json:"total"`
}

// duckduckgoResponse mirrors the DuckDuckGo API response shape.
// We only map the fields we actually use.
type duckduckgoResponse struct {
	AbstractText   string `json:"AbstractText"`
	AbstractURL    string `json:"AbstractURL"`
	AbstractSource string `json:"AbstractSource"`
	RelatedTopics  []struct {
		Text     string `json:"Text"`
		FirstURL string `json:"FirstURL"`
		Result   string `json:"Result"`
	} `json:"RelatedTopics"`
}

// WebSearch returns a ready-to-use WebSearchTool.
// Called in main.go: rt.RegisterTool(tools.WebSearch())
func WebSearch() *WebSearchTool {
	return &WebSearchTool{
		client: &http.Client{
			Timeout: 15 * time.Second,
		},
		baseURL: "https://api.duckduckgo.com",
	}
}

// Name returns the tool's identifier.
// Agents list this in their tools: ["web_search"] in agents.yaml.
// This satisfies the Tool interface.
func (t *WebSearchTool) Name() string {
	return "web_search"
}

// Schema describes this tool to the LLM so it knows when and how to call it.
// The description tells the LLM what the tool does.
// The parameters tell the LLM what JSON to produce when calling it.
// This satisfies the Tool interface.
func (t *WebSearchTool) Schema() tools.Schema {
	return tools.Schema{
		Description: "Search the web for current information about a topic. " +
			"Use this when you need facts, recent events, or data you do not already know. " +
			"Returns a list of relevant results with titles, URLs, and snippets.",
		Parameters: map[string]tools.Parameter{
			"query": {
				Type:        "string",
				Description: "The search query. Be specific for better results. Example: 'Go programming language concurrency patterns 2024'",
				Required:    true,
			},
			"max_results": {
				Type:        "number",
				Description: "Maximum number of results to return. Between 1 and 10. Defaults to 5.",
				Required:    false,
			},
		},
	}
}

// Execute runs the web search when the LLM requests it.
// input is the raw JSON the LLM produced — we parse it into webSearchInput.
// Returns raw JSON results that the LLM reads on its next turn.
// This satisfies the Tool interface.
func (t *WebSearchTool) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	// - parse the input the LLM sent us
	var params webSearchInput
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("web_search: invalid input: %w", err)
	}

	if params.Query == "" {
		return nil, fmt.Errorf("web_search: query is required")
	}

	// Apply sensible defaults
	if params.MaxResults <= 0 || params.MaxResults > 10 {
		params.MaxResults = 5
	}

	// — build the DuckDuckGo API URL
	// format=JSON returns structured data
	// no_html=1 strips HTML tags from results
	// skip_disambig=1 goes straight to results, skipping disambiguation pages
	apiURL := fmt.Sprintf(
		t.baseURL+"/?q=%s&format=json&no_html=1&skip_disambig=1",
		url.QueryEscape(params.Query),
	)

	// — make the HTTP request
	// Use the context so the request is cancelled if the agent times out
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("web_search: build request: %w", err)
	}

	// Set a descriptive User-Agent — good practice for API requests
	req.Header.Set("User-Agent", "Routex/1.0 (AI Agent Framework)")

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("web_search: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("web_search: api returned status %d", resp.StatusCode)
	}

	// — read and parse the response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("web_search: read response: %w", err)
	}

	var ddgResp duckduckgoResponse
	if err := json.Unmarshal(body, &ddgResp); err != nil {
		return nil, fmt.Errorf("web_search: parse response: %w", err)
	}

	// — convert DuckDuckGo's format into our clean output format
	results := make([]webSearchResult, 0, params.MaxResults)

	// The abstract is the main answer — add it first if present
	if ddgResp.AbstractText != "" {
		results = append(results, webSearchResult{
			Title:   ddgResp.AbstractSource,
			URL:     ddgResp.AbstractURL,
			Snippet: ddgResp.AbstractText,
		})
	}

	// Add related topics up to MaxResults
	for _, topic := range ddgResp.RelatedTopics {
		if len(results) >= params.MaxResults {
			break
		}
		if topic.Text == "" {
			continue
		}
		results = append(results, webSearchResult{
			Title:   extractTitle(topic.Text),
			URL:     topic.FirstURL,
			Snippet: topic.Text,
		})
	}

	// If DuckDuckGo returned nothing, tell the LLM clearly
	// so it can try a different query rather than silently failing
	if len(results) == 0 {
		output := webSearchOutput{
			Results: []webSearchResult{},
			Query:   params.Query,
			Total:   0,
		}
		return json.Marshal(output)
	}

	output := webSearchOutput{
		Results: results,
		Query:   params.Query,
		Total:   len(results),
	}

	// — marshal to JSON and return to the agent
	return json.Marshal(output)
}

// extractTitle pulls the first sentence or phrase from a DuckDuckGo
// result text to use as a title. DuckDuckGo does not always provide
// a separate title field for related topics.
func extractTitle(text string) string {
	// Take up to 60 characters as a title
	if len(text) <= 60 {
		return text
	}
	// Try to cut at a word boundary
	for i := 60; i > 40; i-- {
		if text[i] == ' ' {
			return text[:i] + "..."
		}
	}
	return text[:60] + "..."
}

// compile-time interface check
var _ tools.Tool = (*WebSearchTool)(nil)
