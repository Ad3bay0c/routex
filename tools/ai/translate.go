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

// TranslateTool translates text between languages using the DeepL API.
//
// DeepL consistently produces higher quality output than LLM-based translation
// for European languages, especially for formal or technical content.
// Source language is detected automatically if not specified.
//
// API key: https://www.deepl.com/pro-api
// Free tier: 500,000 characters/month
// Free API keys end with ":fx" and use a different endpoint automatically.
//
// agents.yaml:
//
//	tools:
//	  - name: "translate"
//	    api_key: "env:DEEPL_API_KEY"
type TranslateTool struct {
	client       *http.Client
	apiKey       string
	freeAPI      bool // free keys end in ":fx" and use api-free.deepl.com
	deepLURL     string
	freeDeepLURL string
}

type translateInput struct {
	Text       string `json:"text"`
	TargetLang string `json:"target_lang"`
	SourceLang string `json:"source_lang,omitempty"`
}

type translateOutput struct {
	TranslatedText string `json:"translated_text"`
	SourceLang     string `json:"source_lang"`
	TargetLang     string `json:"target_lang"`
	CharCount      int    `json:"char_count"`
}

// deepLRequest mirrors the DeepL API request body.
type deepLRequest struct {
	Text       []string `json:"text"`
	TargetLang string   `json:"target_lang"`
	SourceLang string   `json:"source_lang,omitempty"`
}

// deepLResponse mirrors the DeepL API response body.
type deepLResponse struct {
	Translations []struct {
		Text               string `json:"text"`
		DetectedSourceLang string `json:"detected_source_language"`
	} `json:"translations"`
}

// Translate returns a ready-to-use TranslateTool configured with the given DeepL API key.
// Keys ending in ":fx" automatically use the free API endpoint.
func Translate(apiKey string) *TranslateTool {
	return &TranslateTool{
		client:       &http.Client{Timeout: 15 * time.Second},
		apiKey:       apiKey,
		freeAPI:      strings.HasSuffix(apiKey, ":fx"),
		deepLURL:     "https://api.deepl.com/v2/translate",
		freeDeepLURL: "https://api-free.deepl.com/v2/translate",
	}
}

// Name returns the tool identifier.
// This satisfies the tools.Tool interface.
func (t *TranslateTool) Name() string {
	return "translate"
}

// Schema describes this tool to the LLM.
// This satisfies the tools.Tool interface.
func (t *TranslateTool) Schema() tools.Schema {
	return tools.Schema{
		Description: "Translate text between languages using DeepL. " +
			"Supports 30+ languages with high quality output — better than LLM translation " +
			"for European languages and formal/technical content. " +
			"Source language is auto-detected if not specified.",
		Parameters: map[string]tools.Parameter{
			"text": {
				Type:        "string",
				Description: "The text to translate.",
				Required:    true,
			},
			"target_lang": {
				Type: "string",
				Description: "Target language code. Examples: 'EN-US', 'EN-GB', 'FR', 'DE', " +
					"'ES', 'IT', 'PT-BR', 'PT-PT', 'NL', 'PL', 'JA', 'ZH', 'RU'. " +
					"See DeepL docs for the full list.",
				Required: true,
			},
			"source_lang": {
				Type:        "string",
				Description: "Source language code. Auto-detected if omitted. Example: 'EN', 'FR', 'DE'",
				Required:    false,
			},
		},
	}
}

// Execute translates the given text when the LLM requests it.
// This satisfies the tools.Tool interface.
func (t *TranslateTool) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	// — parse the LLM's input
	var params translateInput
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("translate: invalid input: %w", err)
	}
	if strings.TrimSpace(params.Text) == "" {
		return nil, fmt.Errorf("translate: text is required")
	}
	if strings.TrimSpace(params.TargetLang) == "" {
		return nil, fmt.Errorf("translate: target_lang is required")
	}

	// — choose the right endpoint
	// Free API keys (ending in :fx) use api-free.deepl.com
	// Paid keys use api.deepl.com
	endpoint := t.deepLURL
	if t.freeAPI {
		endpoint = t.freeDeepLURL
	}

	// — build the request
	reqBody := deepLRequest{
		Text:       []string{params.Text},
		TargetLang: strings.ToUpper(params.TargetLang),
	}
	if params.SourceLang != "" {
		reqBody.SourceLang = strings.ToUpper(params.SourceLang)
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("translate: build request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("translate: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "DeepL-Auth-Key "+t.apiKey)

	// — make the call
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("translate: request failed: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	// — handle errors with clear messages
	switch resp.StatusCode {
	case http.StatusForbidden:
		return nil, fmt.Errorf("translate: invalid API key — check DEEPL_API_KEY")
	case http.StatusTooManyRequests:
		return nil, fmt.Errorf("translate: rate limit exceeded — try again later")
	case http.StatusPaymentRequired:
		return nil, fmt.Errorf("translate: DeepL quota exceeded — check your account balance")
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("translate: api returned status %d: %s", resp.StatusCode, body)
	}

	// — parse the response
	var apiResp deepLResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("translate: parse response: %w", err)
	}
	if len(apiResp.Translations) == 0 {
		return nil, fmt.Errorf("translate: empty response from API")
	}

	translation := apiResp.Translations[0]

	sourceLang := translation.DetectedSourceLang
	if params.SourceLang != "" {
		sourceLang = strings.ToUpper(params.SourceLang)
	}

	return json.Marshal(translateOutput{
		TranslatedText: translation.Text,
		SourceLang:     sourceLang,
		TargetLang:     strings.ToUpper(params.TargetLang),
		CharCount:      len(params.Text),
	})
}

// init registers TranslateTool as a built-in.
func init() {
	tools.RegisterBuiltin("translate", func(cfg tools.ToolConfig) (tools.Tool, error) {
		if cfg.APIKey == "" {
			return nil, fmt.Errorf(
				"translate requires an api_key\n" +
					"  add to agents.yaml:  api_key: \"env:DEEPL_API_KEY\"\n" +
					"  then set the env:    export DEEPL_API_KEY=your-key",
			)
		}
		return Translate(cfg.APIKey), nil
	})
}

// compile-time interface check
var _ tools.Tool = (*TranslateTool)(nil)
