package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Ad3bay0c/routex/memory"
	"github.com/Ad3bay0c/routex/tools"
	"github.com/google/uuid"
)

const geminiDefaultAPIRoot = "https://generativelanguage.googleapis.com"

// https://ai.google.dev/api/generate-content

type geminiRequest struct {
	SystemInstruction *geminiContent          `json:"systemInstruction,omitempty"`
	Contents          []geminiContent         `json:"contents"`
	Tools             []geminiToolGroup       `json:"tools,omitempty"`
	GenerationConfig  *geminiGenerationConfig `json:"generationConfig,omitempty"`
}

type geminiGenerationConfig struct {
	Temperature     float64 `json:"temperature,omitempty"`
	MaxOutputTokens int     `json:"maxOutputTokens,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text             string          `json:"text,omitempty"`
	FunctionCall     *geminiFuncCall `json:"functionCall,omitempty"`
	FunctionResponse *geminiFuncResp `json:"functionResponse,omitempty"`
}

type geminiFuncCall struct {
	ID   string          `json:"id,omitempty"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

type geminiFuncResp struct {
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

type geminiToolGroup struct {
	FunctionDeclarations []geminiFunctionDecl `json:"functionDeclarations"`
}

type geminiFunctionDecl struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters"`
}

type geminiResponse struct {
	Candidates []struct {
		Content struct {
			Parts []geminiPart `json:"parts"`
			Role  string       `json:"role"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	UsageMetadata struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
	} `json:"usageMetadata"`
	Error *geminiError `json:"error,omitempty"`
}

type geminiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Status  string `json:"status"`
}

func (e *geminiError) Error() string {
	return fmt.Sprintf("gemini %s: %s", e.Status, e.Message)
}

// GeminiAdapter calls the Google Gemini generateContent API over HTTP.
type GeminiAdapter struct {
	apiKey      string
	endpoint    string
	model       string
	maxTokens   int
	temperature float64
	timeout     time.Duration
	http        *http.Client
}

// NewGeminiAdapter creates a Gemini adapter from a Config.
func NewGeminiAdapter(cfg Config) (*GeminiAdapter, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("gemini: api_key is required")
	}
	if cfg.Model == "" {
		return nil, fmt.Errorf("gemini: model is required — example: gemini-2.0-flash")
	}

	endpoint := cfg.BaseURL
	if endpoint == "" {
		endpoint = fmt.Sprintf("%s/v1beta/models/%s:generateContent",
			geminiDefaultAPIRoot, cfg.Model)
	} else if !strings.Contains(endpoint, ":generateContent") {
		endpoint = fmt.Sprintf("%s/v1beta/models/%s:generateContent",
			strings.TrimRight(endpoint, "/"), cfg.Model)
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

	return &GeminiAdapter{
		apiKey:      cfg.APIKey,
		endpoint:    endpoint,
		model:       cfg.Model,
		maxTokens:   maxTokens,
		temperature: temperature,
		timeout:     timeout,
		http:        &http.Client{Timeout: timeout},
	}, nil
}

func (a *GeminiAdapter) Model() string    { return a.model }
func (a *GeminiAdapter) Provider() string { return "gemini" }

// Complete sends a conversation to Gemini and returns the response.
func (a *GeminiAdapter) Complete(ctx context.Context, req Request) (Response, error) {
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

	apiReq := geminiRequest{
		Contents: buildGeminiContents(req.History),
		Tools:    buildGeminiTools(req.ToolSchemas),
		GenerationConfig: &geminiGenerationConfig{
			Temperature:     temperature,
			MaxOutputTokens: maxTokens,
		},
	}
	if req.SystemPrompt != "" {
		apiReq.SystemInstruction = &geminiContent{
			Parts: []geminiPart{{Text: req.SystemPrompt}},
		}
	}

	body, err := json.Marshal(apiReq)
	if err != nil {
		return Response{}, fmt.Errorf("gemini: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.endpoint, bytes.NewReader(body))
	if err != nil {
		return Response{}, fmt.Errorf("gemini: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-goog-api-key", a.apiKey)

	httpResp, err := a.http.Do(httpReq)
	if err != nil {
		return Response{}, fmt.Errorf("gemini: http: %w", err)
	}
	defer httpResp.Body.Close() //nolint:errcheck

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return Response{}, fmt.Errorf("gemini: read response: %w", err)
	}

	var apiResp geminiResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return Response{}, fmt.Errorf("gemini: decode response: %w", err)
	}

	if apiResp.Error != nil {
		return Response{}, apiResp.Error
	}
	if httpResp.StatusCode != http.StatusOK {
		return Response{}, fmt.Errorf("gemini: HTTP %d: %s", httpResp.StatusCode, string(respBody))
	}

	return translateGeminiResponse(apiResp), nil
}

// buildGeminiContents maps memory.Message history to Gemini contents.
// Roles are "user" and "model". Tool results use functionResponse parts.
func buildGeminiContents(history []memory.Message) []geminiContent {
	contents := make([]geminiContent, 0, len(history))

	for _, msg := range history {
		switch msg.Role {

		case "user":
			if msg.ToolCall == nil {
				contents = append(contents, geminiContent{
					Role:  "user",
					Parts: []geminiPart{{Text: msg.Content}},
				})
				continue
			}
			contents = append(contents, geminiContent{
				Role: "user",
				Parts: []geminiPart{{
					FunctionResponse: &geminiFuncResp{
						Name:     msg.ToolCall.ToolName,
						Response: toolResultToGeminiResponse(msg.ToolCall.Output, msg.ToolCall.Error),
					},
				}},
			})

		case "assistant":
			if len(msg.ToolCalls) > 0 {
				parts := make([]geminiPart, 0, len(msg.ToolCalls))
				for _, tc := range msg.ToolCalls {
					parts = append(parts, geminiPart{
						FunctionCall: toolCallToGeminiFuncCall(tc.ID, tc.ToolName, tc.Input),
					})
				}
				contents = append(contents, geminiContent{Role: "model", Parts: parts})
				continue
			}

			if msg.ToolCall != nil {
				contents = append(contents, geminiContent{
					Role: "model",
					Parts: []geminiPart{{
						FunctionCall: toolCallToGeminiFuncCall(msg.ToolCall.ID, msg.ToolCall.ToolName, msg.ToolCall.Input),
					}},
				})
				continue
			}

			contents = append(contents, geminiContent{
				Role:  "model",
				Parts: []geminiPart{{Text: msg.Content}},
			})
		}
	}

	return contents
}

func toolCallToGeminiFuncCall(id, name, input string) *geminiFuncCall {
	args := json.RawMessage(input)
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	call := &geminiFuncCall{Name: name, Args: args}
	if id != "" {
		call.ID = id
	}
	return call
}

func toolResultToGeminiResponse(output, errMsg string) map[string]any {
	if errMsg != "" {
		return map[string]any{"error": errMsg, "output": output}
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(output), &parsed); err == nil && len(parsed) > 0 {
		return parsed
	}
	return map[string]any{"result": output}
}

func buildGeminiTools(schemas map[string]tools.Schema) []geminiToolGroup {
	if len(schemas) == 0 {
		return nil
	}

	decls := make([]geminiFunctionDecl, 0, len(schemas))
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
		params := map[string]any{
			"type":       "object",
			"properties": properties,
		}
		if len(required) > 0 {
			params["required"] = required
		}
		decls = append(decls, geminiFunctionDecl{
			Name:        name,
			Description: schema.Description,
			Parameters:  params,
		})
	}

	return []geminiToolGroup{{FunctionDeclarations: decls}}
}

func translateGeminiResponse(apiResp geminiResponse) Response {
	resp := Response{
		Usage: TokenUsage{
			InputTokens:  apiResp.UsageMetadata.PromptTokenCount,
			OutputTokens: apiResp.UsageMetadata.CandidatesTokenCount,
		},
	}

	if len(apiResp.Candidates) == 0 {
		resp.FinishReason = "no_candidates"
		return resp
	}

	candidate := apiResp.Candidates[0]
	resp.FinishReason = candidate.FinishReason

	for _, part := range candidate.Content.Parts {
		if part.Text != "" {
			resp.Content = part.Text
		}
		if part.FunctionCall != nil {
			input := string(part.FunctionCall.Args)
			if input == "" {
				input = "{}"
			}
			id := part.FunctionCall.ID
			if id == "" {
				id = "call_" + uuid.New().String()
			}
			resp.ToolCalls = append(resp.ToolCalls, ToolCallRequest{
				ID:       id,
				ToolName: part.FunctionCall.Name,
				Input:    input,
			})
		}
	}

	return resp
}

var _ Adapter = (*GeminiAdapter)(nil)
