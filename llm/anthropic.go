package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Ad3bay0c/routex/memory"
	"github.com/Ad3bay0c/routex/tools"
)

const (
	anthropicDefaultBaseURL = "https://api.anthropic.com/v1/messages"
	anthropicVersion        = "2023-06-01"
)

// https://docs.anthropic.com/en/api/messages

type anthropicRequest struct {
	Model       string             `json:"model"`
	MaxTokens   int                `json:"max_tokens"`
	System      string             `json:"system,omitempty"`
	Messages    []anthropicMessage `json:"messages"`
	Tools       []anthropicTool    `json:"tools,omitempty"`
	Temperature float64            `json:"temperature,omitempty"`
}

type anthropicMessage struct {
	Role    string             `json:"role"`
	Content []anthropicContent `json:"content"`
}

type anthropicContent struct {
	// For text blocks (role=assistant, type=text)
	// For tool_use blocks (role=assistant, type=tool_use)
	Type string `json:"type"`
	Text string `json:"text,omitempty"`

	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	// For tool_result blocks (role=user, type=tool_result)
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`
}

type anthropicTool struct {
	Name        string              `json:"name"`
	Description string              `json:"description,omitempty"`
	InputSchema anthropicToolSchema `json:"input_schema"`
}

type anthropicToolSchema struct {
	Type       string         `json:"type"`
	Properties map[string]any `json:"properties,omitempty"`
	Required   []string       `json:"required,omitempty"`
}

type anthropicResponse struct {
	ID         string             `json:"id"`
	Type       string             `json:"type"`
	Role       string             `json:"role"`
	Content    []anthropicContent `json:"content"`
	Model      string             `json:"model"`
	StopReason string             `json:"stop_reason"`
	Usage      struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Error *anthropicError `json:"error,omitempty"`
}

type anthropicError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

func (e *anthropicError) Error() string {
	return fmt.Sprintf("anthropic %s: %s", e.Type, e.Message)
}

// AnthropicAdapter calls the Anthropic Messages API directly over HTTP.
// No SDK — just net/http and encoding/json. Zero third-party dependencies.
type AnthropicAdapter struct {
	apiKey      string
	baseURL     string
	model       string
	maxTokens   int
	temperature float64
	timeout     time.Duration
	http        *http.Client
}

// NewAnthropicAdapter creates an Anthropic adapter from a Config.
func NewAnthropicAdapter(cfg Config) (*AnthropicAdapter, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("anthropic: api_key is required")
	}

	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = anthropicDefaultBaseURL
	}

	maxTokens := cfg.MaxTokens
	if maxTokens == 0 {
		maxTokens = 4096
	}

	temperature := cfg.Temperature
	if temperature == 0 {
		temperature = 0.7
	}

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 120 * time.Second
	}

	return &AnthropicAdapter{
		apiKey:      cfg.APIKey,
		baseURL:     baseURL,
		model:       cfg.Model,
		maxTokens:   maxTokens,
		temperature: temperature,
		timeout:     timeout,
		http:        &http.Client{Timeout: timeout},
	}, nil
}

func (a *AnthropicAdapter) Model() string    { return a.model }
func (a *AnthropicAdapter) Provider() string { return "anthropic" }

// Complete sends a conversation to Anthropic and returns the response.
func (a *AnthropicAdapter) Complete(ctx context.Context, req Request) (Response, error) {
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = a.maxTokens
	}
	temperature := req.Temperature
	if temperature == 0 {
		temperature = a.temperature
	}

	apiReq := anthropicRequest{
		Model:       a.model,
		MaxTokens:   maxTokens,
		Temperature: temperature,
		System:      req.SystemPrompt,
		Messages:    buildAnthropicMessages(req.History),
		Tools:       buildAnthropicTools(req.ToolSchemas),
	}

	body, err := json.Marshal(apiReq)
	if err != nil {
		return Response{}, fmt.Errorf("anthropic: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL, bytes.NewReader(body))
	if err != nil {
		return Response{}, fmt.Errorf("anthropic: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", a.apiKey)
	httpReq.Header.Set("anthropic-version", anthropicVersion)

	httpResp, err := a.http.Do(httpReq)
	if err != nil {
		return Response{}, fmt.Errorf("anthropic: http: %w", err)
	}
	defer httpResp.Body.Close() //nolint:errcheck

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return Response{}, fmt.Errorf("anthropic: read response: %w", err)
	}

	var apiResp anthropicResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return Response{}, fmt.Errorf("anthropic: decode response: %w", err)
	}

	// API-level error (e.g. auth failure, invalid model) comes back as
	// a 4xx/5xx with an error object in the body
	if apiResp.Error != nil {
		return Response{}, apiResp.Error
	}
	if httpResp.StatusCode != http.StatusOK {
		return Response{}, fmt.Errorf("anthropic: HTTP %d: %s", httpResp.StatusCode, string(respBody))
	}

	return translateAnthropicResponse(apiResp), nil
}

// buildAnthropicMessages converts []memory.Message into the Anthropic
// messages array format.
//
// Multi-tool batches: when an assistant message has ToolCalls (plural),
// we emit one assistant message with all tool_use content blocks, followed
// by one user message per tool result — Anthropic requires all results
// before the next LLM call.
func buildAnthropicMessages(history []memory.Message) []anthropicMessage {
	msgs := make([]anthropicMessage, 0, len(history))

	for _, msg := range history {
		switch msg.Role {

		case "user":
			if msg.ToolCall == nil {
				msgs = append(msgs, anthropicMessage{
					Role: "user",
					Content: []anthropicContent{
						{Type: "text", Text: msg.Content},
					},
				})
				continue
			}
			// Single tool result
			msgs = append(msgs, anthropicMessage{
				Role: "user",
				Content: []anthropicContent{{
					Type:      "tool_result",
					ToolUseID: msg.ToolCall.ID,
					Content:   msg.ToolCall.Output,
					IsError:   msg.ToolCall.Error != "",
				}},
			})

		case "assistant":
			// Multi-tool batch — one assistant message with all tool_use blocks
			if len(msg.ToolCalls) > 0 {
				blocks := make([]anthropicContent, 0, len(msg.ToolCalls))
				for _, tc := range msg.ToolCalls {
					blocks = append(blocks, anthropicContent{
						Type:  "tool_use",
						ID:    tc.ID,
						Name:  tc.ToolName,
						Input: json.RawMessage(tc.Input),
					})
				}
				msgs = append(msgs, anthropicMessage{Role: "assistant", Content: blocks})
				continue
			}

			if msg.ToolCall != nil {
				// Single tool use request
				msgs = append(msgs, anthropicMessage{
					Role: "assistant",
					Content: []anthropicContent{{
						Type:  "tool_use",
						ID:    msg.ToolCall.ID,
						Name:  msg.ToolCall.ToolName,
						Input: json.RawMessage(msg.ToolCall.Input),
					}},
				})
				continue
			}

			msgs = append(msgs, anthropicMessage{
				Role: "assistant",
				Content: []anthropicContent{
					{Type: "text", Text: msg.Content},
				},
			})
		}
	}

	return msgs
}

// buildAnthropicTools converts our tool schema map into Anthropic's tools array format.
func buildAnthropicTools(schemas map[string]tools.Schema) []anthropicTool {
	if len(schemas) == 0 {
		return nil
	}

	result := make([]anthropicTool, 0, len(schemas))
	for name, schema := range schemas {
		properties := make(map[string]any, len(schema.Parameters))
		var required []string

		for paramName, param := range schema.Parameters {
			properties[paramName] = map[string]any{
				"type":        param.Type,
				"description": param.Description,
			}
			if param.Required {
				required = append(required, paramName)
			}
		}

		result = append(result, anthropicTool{
			Name:        name,
			Description: schema.Description,
			InputSchema: anthropicToolSchema{
				Type:       "object",
				Properties: properties,
				Required:   required,
			},
		})
	}
	return result
}

// translateAnthropicResponse converts the Anthropic API response into
// our clean Response type. Collects all tool_use blocks so the agent
// can execute them concurrently.
func translateAnthropicResponse(msg anthropicResponse) Response {
	resp := Response{
		FinishReason: msg.StopReason,
		Usage: TokenUsage{
			InputTokens:  msg.Usage.InputTokens,
			OutputTokens: msg.Usage.OutputTokens,
		},
	}

	for _, block := range msg.Content {
		switch block.Type {
		case "text":
			resp.Content = block.Text

		case "tool_use":
			// Input comes back as a raw JSON object — convert to string
			input := string(block.Input)
			if input == "" {
				input = "{}"
			}
			resp.ToolCalls = append(resp.ToolCalls, ToolCallRequest{
				ID:       block.ID,
				ToolName: block.Name,
				Input:    input,
			})
		}
	}

	return resp
}

// compile-time interface check
var _ Adapter = (*AnthropicAdapter)(nil)
