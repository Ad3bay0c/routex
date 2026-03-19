package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/sashabaranov/go-openai"

	"github.com/Ad3bay0c/routex/memory"
	"github.com/Ad3bay0c/routex/tools"
)

// OpenAIAdapter implements the Adapter interface using the OpenAI API.
// Also works with any OpenAI-compatible API — Groq, Together AI,
// Mistral, and others all speak the same protocol.
// Point BaseURL at the compatible endpoint and it just works.
type OpenAIAdapter struct {
	client      *openai.Client
	model       string
	maxTokens   int
	temperature float64
	timeout     time.Duration
}

// NewOpenAIAdapter creates an OpenAI adapter from a Config.
// Called by llm.New() when provider is "openai".
func NewOpenAIAdapter(cfg Config) (*OpenAIAdapter, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("openai: api_key is required")
	}

	// go-openai uses a ClientConfig struct rather than functional options
	clientCfg := openai.DefaultConfig(cfg.APIKey)

	// Override the base URL if set — this is how you point at
	// Groq, Together AI, or any other OpenAI-compatible provider
	if cfg.BaseURL != "" {
		clientCfg.BaseURL = cfg.BaseURL
	}

	client := openai.NewClientWithConfig(clientCfg)

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
		client:      client,
		model:       cfg.Model,
		maxTokens:   maxTokens,
		temperature: temperature,
		timeout:     timeout,
	}, nil
}

// Complete sends a conversation to OpenAI and returns the response.
// Identical contract to AnthropicAdapter.Complete() — same six steps,
// different SDK calls underneath.
//
// This satisfies the Adapter interface.
func (a *OpenAIAdapter) Complete(ctx context.Context, req Request) (Response, error) {
	// Apply per-call timeout
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	// Translate our history into OpenAI's message format
	messages := buildOpenAIMessages(req.SystemPrompt, req.History)

	// Translate our tool schemas into OpenAI's tool format
	openAITools := buildOpenAITools(req.ToolSchemas)

	// Choose effective token and temperature values
	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = a.maxTokens
	}

	temperature := req.Temperature
	if temperature == 0 {
		temperature = a.temperature
	}

	// Build the API request
	// OpenAI puts the system prompt inside the messages array
	// as a message with role "system" — different from Anthropic
	// which has a dedicated System field
	chatReq := openai.ChatCompletionRequest{
		Model:       a.model,
		Messages:    messages,
		MaxTokens:   maxTokens,
		Temperature: float32(temperature),
	}

	// Only attach tools if there are any
	if len(openAITools) > 0 {
		chatReq.Tools = openAITools
	}

	// Make the API call
	resp, err := a.client.CreateChatCompletion(ctx, chatReq)
	if err != nil {
		return Response{}, fmt.Errorf("openai: api call failed: %w", err)
	}

	// Translate OpenAI's response back into our Response type
	return translateOpenAIResponse(resp), nil
}

// Model returns the model string this adapter is using.
// This satisfies the Adapter interface.
func (a *OpenAIAdapter) Model() string {
	return a.model
}

// Provider returns "openai".
// This satisfies the Adapter interface.
func (a *OpenAIAdapter) Provider() string {
	return "openai"
}

// buildOpenAIMessages converts our history into OpenAI's
// []ChatCompletionMessage format.
//
// Key difference from Anthropic: OpenAI puts the system prompt
// as the very first message in the array with role "system".
// Tool results use role "tool" with a ToolCallID linking back
// to the assistant message that requested the tool call.
func buildOpenAIMessages(systemPrompt string, history []memory.Message) []openai.ChatCompletionMessage {
	messages := make([]openai.ChatCompletionMessage, 0, len(history)+1)

	// System prompt is always first in OpenAI's format
	if systemPrompt != "" {
		messages = append(messages, openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleSystem,
			Content: systemPrompt,
		})
	}

	for _, msg := range history {
		switch msg.Role {

		case "user":
			if msg.ToolCall == nil {
				// Plain user message
				messages = append(messages, openai.ChatCompletionMessage{
					Role:    openai.ChatMessageRoleUser,
					Content: msg.Content,
				})
				continue
			}

			// Tool result — OpenAI uses role "tool" with a ToolCallID
			// The ToolCallID must match the ID from the assistant's tool_calls
			messages = append(messages, openai.ChatCompletionMessage{
				Role:       openai.ChatMessageRoleTool,
				Content:    msg.ToolCall.Output,
				ToolCallID: msg.ToolCall.ToolName,
			})

		case "assistant":
			if msg.ToolCall == nil {
				// Plain assistant text response
				messages = append(messages, openai.ChatCompletionMessage{
					Role:    openai.ChatMessageRoleAssistant,
					Content: msg.Content,
				})
				continue
			}

			// Assistant requesting a tool call
			// OpenAI uses a ToolCalls array on the assistant message
			messages = append(messages, openai.ChatCompletionMessage{
				Role:    openai.ChatMessageRoleAssistant,
				Content: "",
				ToolCalls: []openai.ToolCall{
					{
						ID:   msg.ToolCall.ToolName,
						Type: openai.ToolTypeFunction,
						Function: openai.FunctionCall{
							Name:      msg.ToolCall.ToolName,
							Arguments: msg.ToolCall.Input,
						},
					},
				},
			})
		}
	}

	return messages
}

// buildOpenAITools converts our tool schemas into OpenAI's
// []Tool format. OpenAI wraps tools in a "function" type
// with a separate "function" sub-object — more nested than Anthropic.
func buildOpenAITools(schemas map[string]tools.Schema) []openai.Tool {
	if len(schemas) == 0 {
		return nil
	}

	openAITools := make([]openai.Tool, 0, len(schemas))

	for name, schema := range schemas {
		// Build the parameters JSON schema
		properties := make(map[string]any, len(schema.Parameters))
		required := make([]string, 0)

		for paramName, param := range schema.Parameters {
			properties[paramName] = map[string]any{
				"type":        param.Type,
				"description": param.Description,
			}
			if param.Required {
				required = append(required, paramName)
			}
		}

		// OpenAI requires the parameters as a JSON schema object
		// marshalled then stored as a json.RawMessage
		paramsSchema := map[string]any{
			"type":       "object",
			"properties": properties,
		}
		if len(required) > 0 {
			paramsSchema["required"] = required
		}

		paramsJSON, err := json.Marshal(paramsSchema)
		if err != nil {
			// If marshalling fails, use an empty schema rather than crashing
			paramsJSON = []byte(`{"type":"object","properties":{}}`)
		}

		openAITools = append(openAITools, openai.Tool{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        name,
				Description: schema.Description,
				Parameters:  json.RawMessage(paramsJSON),
			},
		})
	}

	return openAITools
}

// translateOpenAIResponse converts an OpenAI ChatCompletionResponse
// into our clean Response type.
//
// OpenAI always returns at least one Choice. We read the first choice
// and check whether the finish reason is "tool_calls" or "stop".
func translateOpenAIResponse(resp openai.ChatCompletionResponse) Response {
	result := Response{
		Usage: TokenUsage{
			InputTokens:  resp.Usage.PromptTokens,
			OutputTokens: resp.Usage.CompletionTokens,
		},
	}

	// Guard — should never happen but protects against empty responses
	if len(resp.Choices) == 0 {
		result.FinishReason = "no_choices"
		return result
	}

	choice := resp.Choices[0]
	result.FinishReason = string(choice.FinishReason)

	// Check if the model wants to call a tool
	if len(choice.Message.ToolCalls) > 0 {
		toolCall := choice.Message.ToolCalls[0] // handle one tool per turn

		result.ToolCall = &ToolCallRequest{
			ID:       toolCall.ID,
			ToolName: toolCall.Function.Name,
			Input:    toolCall.Function.Arguments,
		}
		return result
	}

	// No tool call — the model produced a text response
	result.Content = choice.Message.Content
	return result
}

// compile-time interface check
var _ Adapter = (*OpenAIAdapter)(nil)
