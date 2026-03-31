package search

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Ad3bay0c/routex/tools"
)

// WikipediaTool fetches article summaries from Wikipedia's REST API.
//
// No API key required. Wikipedia's REST API is free, stable, and fast.
// It returns the opening summary of any article — perfect for agents
// that need factual background on a topic without reading the full page.
//
// Supports all Wikipedia language editions (en, fr, de, es, zh, etc.)
//
// agents.yaml:
//
//	tools:
//	  - name: "wikipedia"
//	    extra:
//	      language: "en"   # optional, defaults to English
type WikipediaTool struct {
	client   *http.Client
	language string
	baseURL  string
}

// wikipediaInput is the JSON the LLM sends when calling this tool.
type wikipediaInput struct {
	Topic    string `json:"topic"`
	Language string `json:"language,omitempty"`
	MaxChars int    `json:"max_chars,omitempty"`
}

// wikipediaOutput is the response sent back to the LLM.
type wikipediaOutput struct {
	Title     string `json:"title"`
	Summary   string `json:"summary"`
	URL       string `json:"url"`
	Truncated bool   `json:"truncated"`
}

// wikipediaSummaryResponse mirrors the Wikipedia REST API /page/summary response.
// Only the fields we use are mapped — the full response has many more.
type wikipediaSummaryResponse struct {
	Title       string `json:"title"`
	Extract     string `json:"extract"` // plain text summary
	ContentURLs struct {
		Desktop struct {
			Page string `json:"page"`
		} `json:"desktop"`
	} `json:"content_urls"`
}

// Wikipedia returns a ready-to-use WikipediaTool defaulting to English.
func Wikipedia() *WikipediaTool {
	return &WikipediaTool{
		client:   &http.Client{Timeout: 15 * time.Second},
		language: "en",
	}
}

// Name returns the tool identifier.
// This satisfies the Tool interface.
func (t *WikipediaTool) Name() string {
	return "wikipedia"
}

// Schema describes this tool to the LLM.
// This satisfies the Tool interface.
func (t *WikipediaTool) Schema() tools.Schema {
	return tools.Schema{
		Description: "Fetch a factual summary of any topic from Wikipedia. " +
			"Use for definitions, background context, historical facts, and overviews. " +
			"Free, no API key needed, available in 300+ languages. " +
			"Best for well-known topics — obscure topics may return not found.",
		Parameters: map[string]tools.Parameter{
			"topic": {
				Type: "string",
				Description: "The topic to look up. Use the Wikipedia article title for best results. " +
					"Examples: 'Large language model', 'Go programming language', 'Goroutine'",
				Required: true,
			},
			"language": {
				Type:        "string",
				Description: "Wikipedia language edition code. Default: 'en'. Examples: 'fr', 'de', 'es', 'zh', 'ja'",
				Required:    false,
			},
			"max_chars": {
				Type:        "number",
				Description: "Maximum characters of the summary to return. Default: 3000.",
				Required:    false,
			},
		},
	}
}

// Execute fetches a Wikipedia article summary when the LLM requests it.
// This satisfies the Tool interface.
func (t *WikipediaTool) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	// — parse the LLM's input
	var params wikipediaInput
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("wikipedia: invalid input: %w", err)
	}
	if params.Topic == "" {
		return nil, fmt.Errorf("wikipedia: topic is required")
	}

	lang := params.Language
	if lang == "" {
		lang = t.language
	}
	maxChars := params.MaxChars
	if maxChars <= 0 {
		maxChars = 3000
	}

	// — build the Wikipedia REST API URL
	// The /page/summary endpoint resolves redirects and disambiguation automatically.
	// We replace spaces with underscores — Wikipedia's URL convention.
	topic := strings.ReplaceAll(strings.TrimSpace(params.Topic), " ", "_")
	baseURL := t.baseURL
	if baseURL == "" {
		baseURL = fmt.Sprintf("https://%s.wikipedia.org", lang)
	}
	apiURL := fmt.Sprintf(
		"%s/api/rest_v1/page/summary/%s",
		baseURL, topic,
	)

	// — make the request
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("wikipedia: build request: %w", err)
	}
	req.Header.Set("User-Agent", "Routex/1.0 (AI Agent Framework; https://github.com/Ad3bay0c/routex)")
	req.Header.Set("Accept", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("wikipedia: request failed: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	// — handle errors with helpful messages
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf(
			"wikipedia: article %q not found in %s.wikipedia.org\n"+
				"  try: exact article title, different capitalisation, or different language",
			params.Topic, lang,
		)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("wikipedia: api returned status %d", resp.StatusCode)
	}

	// — parse the response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("wikipedia: read response: %w", err)
	}

	var apiResp wikipediaSummaryResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("wikipedia: parse response: %w", err)
	}

	// — truncate the summary if needed, at a rune boundary
	summary := apiResp.Extract
	truncated := false

	if utf8.RuneCountInString(summary) > maxChars {
		runes := []rune(summary)
		summary = string(runes[:maxChars])
		truncated = true
	}

	return json.Marshal(wikipediaOutput{
		Title:     apiResp.Title,
		Summary:   summary,
		URL:       apiResp.ContentURLs.Desktop.Page,
		Truncated: truncated,
	})
}

// init registers WikipediaTool as a built-in.
// No API key needed — Wikipedia is free.
func init() {
	tools.RegisterBuiltin("wikipedia", func(cfg tools.ToolConfig) (tools.Tool, error) {
		t := Wikipedia()
		// Allow language override from agents.yaml extra: section
		if lang, ok := cfg.Extra["language"]; ok && lang != "" {
			t.language = lang
		}
		return t, nil
	})
}

// compile-time interface check
var _ tools.Tool = (*WikipediaTool)(nil)
