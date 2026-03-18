package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/Ad3bay0c/routex/memory"
	"github.com/Ad3bay0c/routex/tools"
)

// AnthropicAdapter implements the Adapter interface using the Anthropic API.
// It translates Routex's internal Request/Response types into the shapes
// the Anthropic SDK expects, and back again.
//
// Agents never import this file — they hold the Adapter interface.
// Only llm.New() and the compile-time check at the bottom know this type exists.
type AnthropicAdapter struct {
	client      *anthropic.Client
	model       string
	maxTokens   int
	temperature float64
	timeout     time.Duration
}

// NewAnthropicAdapter creates an Anthropic adapter from a Config.
// Called by llm.New() when provider is "anthropic".
func NewAnthropicAdapter(cfg Config) (*AnthropicAdapter, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("anthropic: api_key is required")
	}

	// Build the client with the API key.
	// option.WithAPIKey injects it into every request header automatically.
	// If a custom BaseURL is set (e.g. a proxy), we honour that too.
	clientOpts := []option.RequestOption{
		option.WithAPIKey(cfg.APIKey),
	}
	if cfg.BaseURL != "" {
		clientOpts = append(clientOpts, option.WithBaseURL(cfg.BaseURL))
	}

	client := anthropic.NewClient(clientOpts...)

	// Apply sensible defaults for optional fields
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
		client:      &client,
		model:       cfg.Model,
		maxTokens:   maxTokens,
		temperature: temperature,
		timeout:     timeout,
	}, nil
}

// Complete sends a conversation to Anthropic and returns the response.
// This is the core method — called by the agent on every thinking turn.
//
// The agent's loop looks like this:
//  1. Call Complete() with history + tool schemas
//  2. If response has ToolCall → execute tool → append result → go to 1
//  3. If response has Content  → agent is done, return content
func (a *AnthropicAdapter) Complete(ctx context.Context, req Request) (Response, error) {
	// Apply per-call timeout on top of the context passed in.
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	// translate our history into Anthropic's message format
	messages, err := buildAnthropicMessages(req.History)
	if err != nil {
		return Response{}, fmt.Errorf("anthropic: build messages: %w", err)
	}

	// translate our tool schemas into Anthropic's tool format
	anthropicTools := buildAnthropicTools(req.ToolSchemas)

	// choose effective token and temperature values
	// Per-request values override the adapter defaults
	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = a.maxTokens
	}

	temperature := req.Temperature
	if temperature == 0 {
		temperature = a.temperature
	}

	// build the API request
	params := anthropic.MessageNewParams{
		Model:     a.model,
		MaxTokens: int64(maxTokens),
		System: []anthropic.TextBlockParam{
			{Text: req.SystemPrompt},
		},
		Messages: messages,
	}

	// Only attach tools if there are any — sending an empty tools array
	// to Anthropic causes an API error
	if len(anthropicTools) > 0 {
		params.Tools = anthropicTools
	}

	// make the API call
	msg, err := a.client.Messages.New(ctx, params)
	if err != nil {
		return Response{}, fmt.Errorf("anthropic: api call failed: %w", err)
	}

	// translate Anthropic's response back into our Response type
	return translateAnthropicResponse(msg), nil
}

// Model returns the model string this adapter is using.
// This satisfies the Adapter interface.
func (a *AnthropicAdapter) Model() string {
	return a.model
}

// Provider returns "anthropic".
// This satisfies the Adapter interface.
func (a *AnthropicAdapter) Provider() string {
	return "anthropic"
}

// buildAnthropicMessages converts our []memory.Message into the
// []anthropic.MessageParam format the SDK expects.
//
// Anthropic messages alternate strictly between "user" and "assistant" roles.
// Tool results are sent as "user" messages with a special tool_result block.
// This function handles all those cases.
func buildAnthropicMessages(history []memory.Message) ([]anthropic.MessageParam, error) {
	params := make([]anthropic.MessageParam, 0, len(history))

	for _, msg := range history {
		switch msg.Role {

		case "user":
			// Plain user message — just text content
			if msg.ToolCall == nil {
				params = append(params, anthropic.NewUserMessage(
					anthropic.NewTextBlock(msg.Content),
				))
				continue
			}

			// Tool result message — user role with tool_result content block
			// The ToolUseID links this result to the tool_use block that requested it
			params = append(params, anthropic.NewUserMessage(
				anthropic.NewToolResultBlock(
					msg.ToolCall.ToolName,
					msg.ToolCall.Output,
					msg.ToolCall.Error != "",
				),
			))

		case "assistant":
			// Assistant message — could be text or a tool_use request
			if msg.ToolCall == nil {
				// Plain text response from the model
				params = append(params, anthropic.NewAssistantMessage(
					anthropic.NewTextBlock(msg.Content),
				))
				continue
			}

			// Tool use request from the model
			// We need to reconstruct the tool_use content block
			var inputRaw map[string]any
			if err := json.Unmarshal([]byte(msg.ToolCall.Input), &inputRaw); err != nil {
				inputRaw = map[string]any{}
			}

			params = append(params, anthropic.NewAssistantMessage(
				anthropic.NewToolUseBlock(
					msg.ToolCall.ToolName, // tool_use_id
					inputRaw,
					msg.ToolCall.ToolName, // tool name
				),
			))

		default:
			// Skip system messages — they go in the System field, not here
			continue
		}
	}

	return params, nil
}

// buildAnthropicTools converts our map of tool schemas into the
// []anthropic.ToolParam format the SDK expects.
// The LLM reads these to know what tools are available and how to call them.
func buildAnthropicTools(schemas map[string]tools.Schema) []anthropic.ToolUnionParam {
	if len(schemas) == 0 {
		return nil
	}

	anthropicTools := make([]anthropic.ToolUnionParam, 0, len(schemas))

	for name, schema := range schemas {
		// Build the input_schema — describes the JSON object the LLM
		// must produce when calling this tool
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

		inputSchema := anthropic.ToolInputSchemaParam{
			Properties: properties,
		}

		anthropicTools = append(anthropicTools, anthropic.ToolUnionParam{
			OfTool: &anthropic.ToolParam{
				Name:        name,
				Description: anthropic.String(schema.Description),
				InputSchema: inputSchema,
			},
		})
	}

	return anthropicTools
}

// translateAnthropicResponse converts an Anthropic API response
// into our clean Response type.
//
// Anthropic responses can contain multiple content blocks — a mix of
// text blocks and tool_use blocks. We handle both cases here.
func translateAnthropicResponse(msg *anthropic.Message) Response {
	resp := Response{
		FinishReason: string(msg.StopReason),
		Usage: TokenUsage{
			InputTokens:  int(msg.Usage.InputTokens),
			OutputTokens: int(msg.Usage.OutputTokens),
		},
	}

	// Walk through each content block in the response
	for _, block := range msg.Content {
		switch block.Type {

		case "text":
			// The model produced a text response — the agent is done this turn
			resp.Content = block.Text

		case "tool_use":
			// The model wants to call a tool — give it what it needs
			// We serialise the input back to JSON so the tool registry
			// can pass it directly to Tool.Execute()
			inputJSON, err := json.Marshal(block.Input)
			if err != nil {
				inputJSON = []byte("{}")
			}

			resp.ToolCall = &ToolCallRequest{
				ID:       block.ID,
				ToolName: block.Name,
				Input:    string(inputJSON),
			}

			// Once we find a tool_use block, stop — agents handle one
			// tool call per turn, then call Complete() again with the result
			return resp
		}
	}

	return resp
}

// compile-time interface check
var _ Adapter = (*AnthropicAdapter)(nil)
