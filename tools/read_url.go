package tools

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
)

func init() {
	RegisterBuiltin("read_url", func(cfg ToolConfig) (Tool, error) {
		return ReadURL(), nil
	})
}

// ReadURLTool fetches the content of a webpage and returns it as plain text.
// Used by researcher and writer agents to read source material from the web.
// Strips HTML tags so the LLM receives clean readable text, not raw markup.
type ReadURLTool struct {
	client   *http.Client
	maxBytes int // maximum response size to read
}

// readURLInput is the shape of JSON the LLM sends when calling this tool.
type readURLInput struct {
	// URL is the webpage to fetch.
	URL string `json:"url"`

	// MaxChars limits how many characters to return to the LLM.
	// Large pages can consume enormous token counts — truncating
	// keeps costs under control. Defaults to 4000 chars.
	MaxChars int `json:"max_chars,omitempty"`
}

// readURLOutput is the response we send back to the LLM.
type readURLOutput struct {
	URL         string `json:"url"`
	Content     string `json:"content"`
	CharCount   int    `json:"char_count"`
	Truncated   bool   `json:"truncated"`
	ContentType string `json:"content_type"`
}

// ReadURL returns a ready-to-use ReadURLTool.
func ReadURL() *ReadURLTool {
	return &ReadURLTool{
		client: &http.Client{
			Timeout: 20 * time.Second,
		},
		maxBytes: 1024 * 1024, // 1MB max response body
	}
}

// Name returns the tool identifier.
// This satisfies the Tool interface.
func (t *ReadURLTool) Name() string {
	return "read_url"
}

// Schema describes this tool to the LLM.
// This satisfies the Tool interface.
func (t *ReadURLTool) Schema() Schema {
	return Schema{
		Description: "Fetch and read the text content of a webpage. " +
			"Use this to read articles, documentation, or any web page in full. " +
			"Returns clean plain text with HTML tags removed.",
		Parameters: map[string]Parameter{
			"url": {
				Type:        "string",
				Description: "The full URL of the page to read. Must start with http:// or https://",
				Required:    true,
			},
			"max_chars": {
				Type:        "number",
				Description: "Maximum characters to return. Defaults to 4000. Increase for longer documents.",
				Required:    false,
			},
		},
	}
}

// Execute fetches a URL and returns its text content.
// This satisfies the Tool interface.
func (t *ReadURLTool) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	// — parse the LLM's input
	var params readURLInput
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("read_url: invalid input: %w", err)
	}

	if params.URL == "" {
		return nil, fmt.Errorf("read_url: url is required")
	}

	// Apply defaults
	if params.MaxChars <= 0 {
		params.MaxChars = 4000
	}

	// — validate the URL
	// Prevent agents from accidentally fetching internal network addresses
	parsed, err := url.Parse(params.URL)
	if err != nil {
		return nil, fmt.Errorf("read_url: invalid url %q: %w", params.URL, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("read_url: only http and https URLs are supported, got %q", parsed.Scheme)
	}

	// — fetch the page
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, params.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("read_url: build request: %w", err)
	}

	// Identify ourselves and request plain text where possible
	req.Header.Set("User-Agent", "Routex/1.0 (AI Agent Framework)")
	req.Header.Set("Accept", "text/html,text/plain,application/xhtml+xml")

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("read_url: fetch %q: %w", params.URL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("read_url: %q returned status %d", params.URL, resp.StatusCode)
	}

	contentType := resp.Header.Get("Content-Type")

	// — read the body with a size limit
	// io.LimitReader caps how many bytes we read — prevents
	// accidentally downloading a 500MB file
	limited := io.LimitReader(resp.Body, int64(t.maxBytes))
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read_url: read body: %w", err)
	}

	// — strip HTML and clean up the text
	text := stripHTML(string(body))
	text = cleanWhitespace(text)

	// — truncate to MaxChars if needed
	truncated := false
	if utf8.RuneCountInString(text) > params.MaxChars {
		// Truncate at a rune boundary — important for non-ASCII content
		runes := []rune(text)
		text = string(runes[:params.MaxChars])
		truncated = true
	}

	output := readURLOutput{
		URL:         params.URL,
		Content:     text,
		CharCount:   utf8.RuneCountInString(text),
		Truncated:   truncated,
		ContentType: contentType,
	}

	return json.Marshal(output)
}

// stripHTML removes HTML tags from a string and decodes common HTML entities.
// This is intentionally simple — for production use consider a proper
// HTML parser like golang.org/x/net/html.
func stripHTML(s string) string {
	var result strings.Builder
	inTag := false

	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
		case !inTag:
			result.WriteRune(r)
		}
	}

	// Decode the most common HTML entities
	text := result.String()
	text = strings.ReplaceAll(text, "&amp;", "&")
	text = strings.ReplaceAll(text, "&lt;", "<")
	text = strings.ReplaceAll(text, "&gt;", ">")
	text = strings.ReplaceAll(text, "&quot;", `"`)
	text = strings.ReplaceAll(text, "&#39;", "'")
	text = strings.ReplaceAll(text, "&nbsp;", " ")

	return text
}

// cleanWhitespace collapses multiple blank lines and leading/trailing
// whitespace so the LLM receives clean readable text rather than
// a wall of empty lines from stripped HTML structure.
func cleanWhitespace(s string) string {
	lines := strings.Split(s, "\n")
	var cleaned []string
	blankCount := 0

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			blankCount++
			// Allow at most one consecutive blank line
			if blankCount <= 1 {
				cleaned = append(cleaned, "")
			}
		} else {
			blankCount = 0
			cleaned = append(cleaned, trimmed)
		}
	}

	return strings.TrimSpace(strings.Join(cleaned, "\n"))
}

// compile-time interface check
var _ Tool = (*ReadURLTool)(nil)
