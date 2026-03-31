package llm

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// OllamaAdapter implements the Adapter interface using a local Ollama server.
//
// Ollama runs open-source models (Llama, Mistral, Gemma, Phi, and many more)
// entirely on your own machine. No API key. No cost per token. No data leaving
// your network. Perfect for development, testing, and privacy-sensitive workloads.
//
// Ollama exposes an OpenAI-compatible API, so OllamaAdapter is a thin wrapper
// around OpenAIAdapter — we reuse all the message building and response
// translation logic we already wrote. This is composition over duplication.
//
// Setup (run once):
//
//	brew install ollama          # macOS
//	ollama serve                 # start the local server
//	ollama pull llama3           # download a model
//
// Then in agents.yaml:
//
//	runtime:
//	  llm_provider: "ollama"
//	  model:        "llama3"    # or mistral, gemma, phi3, etc.
type OllamaAdapter struct {
	// inner is the OpenAI adapter we delegate all real work to.
	// OllamaAdapter exists only to provide a clean entry point,
	// set the right defaults, and verify the server is reachable.
	inner *OpenAIAdapter

	// model and provider are stored separately for the Model() and
	// Provider() methods — inner.Provider() would return "openai"
	// which would be confusing in logs and traces.
	model   string
	baseURL string
}

// defaultOllamaURL is where Ollama listens by default after "ollama serve".
const defaultOllamaURL = "http://localhost:11434/v1"

// NewOllamaAdapter creates an Ollama adapter from a Config.
// Called by llm.New() when provider is "ollama".
//
// No API key is required — Ollama accepts any non-empty string.
// If BaseURL is not set, defaults to http://localhost:11434/v1.
func NewOllamaAdapter(cfg Config) (*OllamaAdapter, error) {
	if cfg.Model == "" {
		return nil, fmt.Errorf("ollama: model is required — run 'ollama list' to see available models")
	}

	// Use default URL if none provided
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = defaultOllamaURL
	}

	// Ollama does not need a real API key but the OpenAI client
	// requires a non-empty string — "ollama" is the conventional placeholder
	apiKey := cfg.APIKey
	if apiKey == "" {
		apiKey = "ollama"
	}

	// Build the inner OpenAI adapter pointed at the local Ollama server.
	// All message formatting, tool calling, and response translation
	// is handled by OpenAIAdapter — we inherit all of that for free.
	inner, err := NewOpenAIAdapter(Config{
		Provider:    "openai", // inner adapter thinks it is talking to OpenAI
		APIKey:      apiKey,
		Model:       cfg.Model,
		BaseURL:     baseURL,
		MaxTokens:   cfg.MaxTokens,
		Temperature: cfg.Temperature,
		Timeout:     cfg.Timeout,
	})
	if err != nil {
		return nil, fmt.Errorf("ollama: %w", err)
	}

	adapter := &OllamaAdapter{
		inner:   inner,
		model:   cfg.Model,
		baseURL: baseURL,
	}

	// Verify the Ollama server is actually running before we return.
	// A clear error here is much better than a cryptic connection refused
	// message later when the first agent tries to think.
	if err := adapter.ping(); err != nil {
		return nil, fmt.Errorf(
			"ollama: server not reachable at %s\n"+
				"  is Ollama running? try: ollama serve\n"+
				"  original error: %w",
			baseURL, err,
		)
	}

	return adapter, nil
}

// Complete delegates entirely to the inner OpenAI adapter.
// Ollama's API is OpenAI-compatible so no translation is needed here.
//
// This satisfies the Adapter interface.
func (a *OllamaAdapter) Complete(ctx context.Context, req Request) (Response, error) {
	resp, err := a.inner.Complete(ctx, req)
	if err != nil {
		// Re-wrap the error with "ollama:" prefix so it is clear
		// in logs that the problem is with the local server,
		// not a remote API call
		return Response{}, fmt.Errorf("ollama: %w", err)
	}
	return resp, nil
}

// Model returns the Ollama model name.
// Example: "llama3", "mistral", "gemma:7b"
//
// This satisfies the Adapter interface.
func (a *OllamaAdapter) Model() string {
	return a.model
}

// Provider returns "ollama" — not "openai" even though we use
// the OpenAI adapter underneath. Logs and traces show the real provider.
//
// This satisfies the Adapter interface.
func (a *OllamaAdapter) Provider() string {
	return "ollama"
}

// ping checks that the Ollama server is reachable.
// We call the /v1/models endpoint — if Ollama is running, it responds.
// If not, we get a connection refused error immediately.
//
// This is called once during NewOllamaAdapter() — not on every Complete() call.
func (a *OllamaAdapter) ping() error {
	// Short timeout for the ping — we just want to know if the server is up
	client := &http.Client{Timeout: 5 * time.Second}

	// Ollama exposes the OpenAI-compatible /v1/models endpoint
	// We use the base URL without /v1 since baseURL already includes it
	url := a.baseURL + "/models"

	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck

	// Any HTTP response means the server is up — even a 404 or 401.
	// We only care that something answered, not what it said.
	return nil
}

// compile-time interface check
var _ Adapter = (*OllamaAdapter)(nil)
