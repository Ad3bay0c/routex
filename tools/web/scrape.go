package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
	"unicode/utf8"

	"github.com/Ad3bay0c/routex/tools"
)

// ScrapeTool fetches web pages including JavaScript-rendered content.
//
// Unlike read_url which only fetches raw HTML, ScrapeTool routes the
// request through a real headless browser — it waits for JS to execute
// before returning the page content. Essential for:
//   - Single-page apps (React, Vue, Angular)
//   - Pages that load content on scroll or after login
//   - Dynamic dashboards and data tables
//
// Uses the ScrapingBee API. Free tier: 1,000 credits/month.
// API key: https://www.scrapingbee.com
//
// agents.yaml:
//
//	tools:
//	  - name: "scrape"
//	    api_key: "env:SCRAPINGBEE_API_KEY"
type ScrapeTool struct {
	client   *http.Client
	apiKey   string
	maxChars int
}

// scrapeInput is the JSON the LLM sends when calling this tool.
type scrapeInput struct {
	URL      string `json:"url"`
	RenderJS bool   `json:"render_js,omitempty"`
	MaxChars int    `json:"max_chars,omitempty"`
}

// scrapeOutput is the response sent back to the LLM.
type scrapeOutput struct {
	URL       string `json:"url"`
	Content   string `json:"content"`
	CharCount int    `json:"char_count"`
	Truncated bool   `json:"truncated"`
	Rendered  bool   `json:"rendered"` // true if JS was executed
}

// Scrape returns a ready-to-use ScrapeTool configured with the given API key.
func Scrape(apiKey string) *ScrapeTool {
	return &ScrapeTool{
		client:   &http.Client{Timeout: 30 * time.Second}, // longer — JS rendering takes time
		apiKey:   apiKey,
		maxChars: 6000,
	}
}

// Name returns the tool identifier.
// This satisfies the Tool interface.
func (t *ScrapeTool) Name() string {
	return "scrape"
}

// Schema describes this tool to the LLM.
// This satisfies the Tool interface.
func (t *ScrapeTool) Schema() tools.Schema {
	return tools.Schema{
		Description: "Fetch the full rendered content of any webpage, including JavaScript-rendered content. " +
			"Use this instead of read_url when the page loads content dynamically (React/Vue apps, SPAs, dashboards). " +
			"Slower than read_url but works on any page. Returns clean plain text with HTML stripped.",
		Parameters: map[string]tools.Parameter{
			"url": {
				Type:        "string",
				Description: "The full URL to fetch. Must start with http:// or https://",
				Required:    true,
			},
			"render_js": {
				Type: "boolean",
				Description: "Wait for JavaScript to execute before extracting content. " +
					"Default: true. Set to false for faster fetching of plain HTML pages.",
				Required: false,
			},
			"max_chars": {
				Type:        "number",
				Description: "Maximum characters to return. Default: 6000.",
				Required:    false,
			},
		},
	}
}

// Execute fetches a URL via ScrapingBee when the LLM requests it.
// This satisfies the Tool interface.
func (t *ScrapeTool) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	// — parse the LLM's input
	var params scrapeInput
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("scrape: invalid input: %w", err)
	}
	if params.URL == "" {
		return nil, fmt.Errorf("scrape: url is required")
	}

	maxChars := params.MaxChars
	if maxChars <= 0 {
		maxChars = t.maxChars
	}

	// render_js defaults to true — that is the primary reason to use this tool
	// over read_url. Only set false when you explicitly want raw HTML speed.
	renderJS := "true"
	if !params.RenderJS {
		renderJS = "false"
	}

	// — build the ScrapingBee API URL
	// ScrapingBee proxies the request through a real Chromium browser.
	// return_page_source=false means we get the rendered DOM, not raw HTML.
	apiURL := fmt.Sprintf(
		"https://app.scrapingbee.com/api/v1/?api_key=%s&url=%s&render_js=%s&return_page_source=false",
		t.apiKey,
		url.QueryEscape(params.URL),
		renderJS,
	)

	// — make the request
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("scrape: build request: %w", err)
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("scrape: request failed: %w", err)
	}
	defer resp.Body.Close()

	// — handle errors
	switch resp.StatusCode {
	case http.StatusForbidden:
		return nil, fmt.Errorf("scrape: invalid API key — check SCRAPINGBEE_API_KEY")
	case http.StatusTooManyRequests:
		return nil, fmt.Errorf("scrape: rate limit exceeded — reduce request frequency or upgrade plan")
	case http.StatusPaymentRequired:
		return nil, fmt.Errorf("scrape: ScrapingBee credits exhausted — check your account balance")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("scrape: api returned status %d", resp.StatusCode)
	}

	// — read with a size cap
	// 2MB limit — ScrapingBee can return large rendered pages
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("scrape: read response: %w", err)
	}

	// — strip HTML tags and clean whitespace
	// ScrapingBee returns rendered HTML — we use the same helpers as read_url
	text := stripHTML(string(body))
	text = cleanWhitespace(text)

	truncated := false
	if utf8.RuneCountInString(text) > maxChars {
		runes := []rune(text)
		text = string(runes[:maxChars])
		truncated = true
	}

	return json.Marshal(scrapeOutput{
		URL:       params.URL,
		Content:   text,
		CharCount: utf8.RuneCountInString(text),
		Truncated: truncated,
		Rendered:  params.RenderJS,
	})
}

// init registers ScrapeTool as a built-in.
// Requires SCRAPINGBEE_API_KEY — fails clearly at startup if missing.
func init() {
	tools.RegisterBuiltin("scrape", func(cfg tools.ToolConfig) (tools.Tool, error) {
		if cfg.APIKey == "" {
			return nil, fmt.Errorf(
				"scrape requires an api_key\n" +
					"  add to agents.yaml:  api_key: \"env:SCRAPINGBEE_API_KEY\"\n" +
					"  then set the env:    export SCRAPINGBEE_API_KEY=your-key",
			)
		}
		return Scrape(cfg.APIKey), nil
	})
}

// compile-time interface check
var _ tools.Tool = (*ScrapeTool)(nil)
