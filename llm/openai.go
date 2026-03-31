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

const openAIDefaultBaseURL = "https://api.openai.com/v1/chat/completions"

// https://platform.openai.com/docs/api-reference/chat

type openAIRequest struct {
	Model               string          `json:"model"`
	Messages            []openAIMessage `json:"messages"`
	MaxCompletionTokens int             `json:"max_completion_tokens,omitempty"`
	Temperature         float64         `json:"temperature,omitempty"`
	Tools               []openAITool    `json:"tools,omitempty"`
}

type openAIMessage struct {
	Role       string           `json:"role"`
	Content    string           `json:"content,omitempty"`
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
	Name       string           `json:"name,omitempty"`
}

type openAIToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"` // always "function"
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"` // JSON string
	} `json:"function"`
}

type openAITool struct {
	Type     string            `json:"type"` // always "function"
	Function openAIFunctionDef `json:"function"`
}

type openAIFunctionDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

type openAIResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Model   string `json:"model"`
	Choices []struct {
		Index        int           `json:"index"`
		Message      openAIMessage `json:"message"`
		FinishReason string        `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Error *openAIError `json:"error,omitempty"`
}

type openAIError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code"`
}

func (e *openAIError) Error() string {
	return fmt.Sprintf("openai %s: %s", e.Type, e.Message)
}

// OpenAIAdapter calls the OpenAI Chat Completions API directly over HTTP.
// No SDK — just net/http and encoding/json. Zero third-party dependencies.
//
// Also works with any OpenAI-compatible API — Groq, Together AI, Mistral,
// and others speak the same protocol. Point BaseURL at the endpoint:
//
//	Config{Provider: "openai", BaseURL: "https://api.groq.com/openai/v1/chat/completions"}
type OpenAIAdapter struct {
	apiKey      string
	baseURL     string
	model       string
	maxTokens   int
	temperature float64
	timeout     time.Duration
	http        *http.Client
}

// NewOpenAIAdapter creates an OpenAI adapter from a Config.
func NewOpenAIAdapter(cfg Config) (*OpenAIAdapter, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("openai: api_key is required")
	}

	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = openAIDefaultBaseURL
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

	return &OpenAIAdapter{
		apiKey:      cfg.APIKey,
		baseURL:     baseURL,
		model:       cfg.Model,
		maxTokens:   maxTokens,
		temperature: temperature,
		timeout:     timeout,
		http:        &http.Client{Timeout: timeout},
	}, nil
}

func (a *OpenAIAdapter) Model() string    { return a.model }
func (a *OpenAIAdapter) Provider() string { return "openai" }

// Complete sends a conversation to OpenAI and returns the response.
func (a *OpenAIAdapter) Complete(ctx context.Context, req Request) (Response, error) {
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

	apiReq := openAIRequest{
		Model:               a.model,
		MaxCompletionTokens: maxTokens,
		Temperature:         temperature,
		Messages:            buildOpenAIMessages(req.SystemPrompt, req.History),
		Tools:               buildOpenAITools(req.ToolSchemas),
	}

	body, err := json.Marshal(apiReq)
	if err != nil {
		return Response{}, fmt.Errorf("openai: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL, bytes.NewReader(body))
	if err != nil {
		return Response{}, fmt.Errorf("openai: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+a.apiKey)

	httpResp, err := a.http.Do(httpReq)
	if err != nil {
		return Response{}, fmt.Errorf("openai: http: %w", err)
	}
	defer httpResp.Body.Close() //nolint:errcheck

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return Response{}, fmt.Errorf("openai: read response: %w", err)
	}

	var apiResp openAIResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return Response{}, fmt.Errorf("openai: decode response: %w", err)
	}

	if apiResp.Error != nil {
		return Response{}, apiResp.Error
	}
	if httpResp.StatusCode != http.StatusOK {
		return Response{}, fmt.Errorf("openai: HTTP %d: %s", httpResp.StatusCode, string(respBody))
	}

	return translateOpenAIResponse(apiResp), nil
}

// buildOpenAIMessages converts []memory.Message into the OpenAI messages
// array format. System prompt goes first as a "system" role message.
//
// Multi-tool batches: one assistant message with all tool_calls entries,
// followed by one "tool" role message per result.
func buildOpenAIMessages(systemPrompt string, history []memory.Message) []openAIMessage {
	msgs := make([]openAIMessage, 0, len(history)+1)

	if systemPrompt != "" {
		msgs = append(msgs, openAIMessage{
			Role:    "system",
			Content: systemPrompt,
		})
	}

	for _, msg := range history {
		switch msg.Role {

		case "user":
			if msg.ToolCall == nil {
				msgs = append(msgs, openAIMessage{
					Role:    "user",
					Content: msg.Content,
				})
				continue
			}
			// Single tool result — role "tool" with ToolCallID
			msgs = append(msgs, openAIMessage{
				Role:       "tool",
				Content:    msg.ToolCall.Output,
				ToolCallID: msg.ToolCall.ID,
			})

		case "assistant":
			// Multi-tool batch — one assistant message with all tool_calls
			if len(msg.ToolCalls) > 0 {
				calls := make([]openAIToolCall, 0, len(msg.ToolCalls))
				for _, tc := range msg.ToolCalls {
					call := openAIToolCall{ID: tc.ID, Type: "function"}
					call.Function.Name = tc.ToolName
					call.Function.Arguments = tc.Input
					calls = append(calls, call)
				}
				msgs = append(msgs, openAIMessage{
					Role:      "assistant",
					ToolCalls: calls,
				})
				continue
			}

			if msg.ToolCall != nil {
				// Single tool call request
				call := openAIToolCall{ID: msg.ToolCall.ID, Type: "function"}
				call.Function.Name = msg.ToolCall.ToolName
				call.Function.Arguments = msg.ToolCall.Input
				msgs = append(msgs, openAIMessage{
					Role:      "assistant",
					ToolCalls: []openAIToolCall{call},
				})
				continue
			}

			msgs = append(msgs, openAIMessage{
				Role:    "assistant",
				Content: msg.Content,
			})
		}
	}

	return msgs
}

// buildOpenAITools converts our tool schema map into OpenAI's tools
// array format. Parameters are marshalled as a JSON Schema object.
func buildOpenAITools(schemas map[string]tools.Schema) []openAITool {
	if len(schemas) == 0 {
		return nil
	}

	result := make([]openAITool, 0, len(schemas))

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

		paramsSchema := map[string]any{
			"type":       "object",
			"properties": properties,
		}
		if len(required) > 0 {
			paramsSchema["required"] = required
		}

		paramsJSON, err := json.Marshal(paramsSchema)
		if err != nil {
			paramsJSON = []byte(`{"type":"object","properties":{}}`)
		}

		result = append(result, openAITool{
			Type: "function",
			Function: openAIFunctionDef{
				Name:        name,
				Description: schema.Description,
				Parameters:  paramsJSON,
			},
		})
	}
	return result
}

// translateOpenAIResponse converts the OpenAI response into our clean
// Response type. Collects all tool_calls so the agent can execute them
// concurrently.
func translateOpenAIResponse(resp openAIResponse) Response {
	result := Response{
		Usage: TokenUsage{
			InputTokens:  resp.Usage.PromptTokens,
			OutputTokens: resp.Usage.CompletionTokens,
		},
	}

	if len(resp.Choices) == 0 {
		result.FinishReason = "no_choices"
		return result
	}

	choice := resp.Choices[0]
	result.FinishReason = choice.FinishReason

	if len(choice.Message.ToolCalls) > 0 {
		result.ToolCalls = make([]ToolCallRequest, 0, len(choice.Message.ToolCalls))
		for _, tc := range choice.Message.ToolCalls {
			result.ToolCalls = append(result.ToolCalls, ToolCallRequest{
				ID:       tc.ID,
				ToolName: tc.Function.Name,
				Input:    tc.Function.Arguments,
			})
		}
		return result
	}

	result.Content = choice.Message.Content
	return result
}

// compile-time interface check
var _ Adapter = (*OpenAIAdapter)(nil)
