package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Ad3bay0c/routex/tools"
)

// SummariseTool compresses long text into a concise summary using Claude Haiku.
//
// Agents use this to shrink scraped content, long documents, or search results
// before passing them downstream — keeping token counts manageable across the crew.
//
// Uses Claude Haiku (fast and cheap) rather than the main runtime model.
// The same ANTHROPIC_API_KEY used for the runtime works here.
//
// agents.yaml:
//
//	tools:
//	  - name: "summarise"
//	    api_key: "env:ANTHROPIC_API_KEY"
type SummariseTool struct {
	client           *http.Client
	apiKey           string
	model            string
	anthropicBaseURL string
}

type summariseInput struct {
	Text     string `json:"text"`
	MaxWords int    `json:"max_words,omitempty"`
	// Style controls the output format:
	//   "paragraph"     — flowing prose (default)
	//   "bullet_points" — bullet list of key points
	//   "one_line"      — single sentence
	Style   string `json:"style,omitempty"`
	FocusOn string `json:"focus_on,omitempty"`
}

type summariseOutput struct {
	Summary   string `json:"summary"`
	WordCount int    `json:"word_count"`
	Style     string `json:"style"`
}

// Summarise returns a ready-to-use SummariseTool using the given Anthropic API key.
func Summarise(apiKey string) *SummariseTool {
	return &SummariseTool{
		client:           &http.Client{Timeout: 30 * time.Second},
		apiKey:           apiKey,
		model:            "claude-haiku-4-5-20251001",
		anthropicBaseURL: "https://api.anthropic.com",
	}
}

// Name returns the tool identifier.
// This satisfies the tools.Tool interface.
func (t *SummariseTool) Name() string {
	return "summarise"
}

// Schema describes this tool to the LLM.
// This satisfies the tools.Tool interface.
func (t *SummariseTool) Schema() tools.Schema {
	return tools.Schema{
		Description: "Summarise a long piece of text into a concise version. " +
			"Use this when scraped content, search results, or documents are too long " +
			"to pass to the next agent. Supports paragraph, bullet point, or one-line styles.",
		Parameters: map[string]tools.Parameter{
			"text": {
				Type:        "string",
				Description: "The text to summarise.",
				Required:    true,
			},
			"max_words": {
				Type:        "number",
				Description: "Target length in words. Default: 150.",
				Required:    false,
			},
			"style": {
				Type:        "string",
				Description: "Output format: 'paragraph' (default), 'bullet_points', or 'one_line'.",
				Required:    false,
			},
			"focus_on": {
				Type:        "string",
				Description: "Optional aspect to focus on. Example: 'key statistics', 'action items', 'main argument'",
				Required:    false,
			},
		},
	}
}

// Execute summarises the given text when the LLM requests it.
// This satisfies the tools.Tool interface.
func (t *SummariseTool) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	// — parse the LLM's input
	var params summariseInput
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("summarise: invalid input: %w", err)
	}
	if strings.TrimSpace(params.Text) == "" {
		return nil, fmt.Errorf("summarise: text is required")
	}

	maxWords := params.MaxWords
	if maxWords <= 0 {
		maxWords = 150
	}
	style := params.Style
	if style == "" {
		style = "paragraph"
	}

	// — build the prompt based on the requested style
	var prompt string
	switch style {
	case "bullet_points":
		prompt = fmt.Sprintf(
			"Summarise the following text as a bullet point list. "+
				"Use at most %d words total across all bullets. "+
				"Each bullet should be one clear, specific point.",
			maxWords,
		)
	case "one_line":
		prompt = "Summarise the following text in exactly one sentence. " +
			"Be concise and capture the single most important idea."
	default:
		prompt = fmt.Sprintf(
			"Summarise the following text in %d words or fewer. "+
				"Write as a single, well-structured paragraph.",
			maxWords,
		)
	}

	if params.FocusOn != "" {
		prompt += fmt.Sprintf(" Focus specifically on: %s.", params.FocusOn)
	}

	prompt += "\n\nText to summarise:\n" + params.Text

	// — call the Anthropic API directly
	// We call the API directly rather than through the runtime's LLM adapter
	// so this tool works regardless of which provider the runtime is using.
	// The summarise tool always uses Anthropic Haiku — fast and cheap.
	reqBody := map[string]any{
		"model":      t.model,
		"max_tokens": 512,
		"messages": []map[string]any{
			{"role": "user", "content": prompt},
		},
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("summarise: build request: %w", err)
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		t.anthropicBaseURL+"/v1/messages",
		bytes.NewReader(bodyBytes),
	)
	if err != nil {
		return nil, fmt.Errorf("summarise: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", t.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	// — make the call
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("summarise: api call failed: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("summarise: invalid API key — check ANTHROPIC_API_KEY")
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("summarise: api returned status %d: %s", resp.StatusCode, body)
	}

	// — parse the response
	var apiResp struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("summarise: parse response: %w", err)
	}

	summary := ""
	for _, block := range apiResp.Content {
		if block.Type == "text" {
			summary = block.Text
			break
		}
	}

	return json.Marshal(summariseOutput{
		Summary:   summary,
		WordCount: countWords(summary),
		Style:     style,
	})
}

// countWords counts the number of words in a string.
func countWords(s string) int {
	return len(strings.Fields(s))
}

// init registers SummariseTool as a built-in.
func init() {
	tools.RegisterBuiltin("summarise", func(cfg tools.ToolConfig) (tools.Tool, error) {
		if cfg.APIKey == "" {
			return nil, fmt.Errorf(
				"summarise requires an api_key\n" +
					"  add to agents.yaml:  api_key: \"env:ANTHROPIC_API_KEY\"\n" +
					"  then set the ANTHROPIC_API_KEY environment variable to your API key",
			)
		}
		return Summarise(cfg.APIKey), nil
	})
}

// compile-time interface check
var _ tools.Tool = (*SummariseTool)(nil)
