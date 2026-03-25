package ai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Ad3bay0c/routex/tools"
)

// GenerateImageTool creates images from text descriptions using the OpenAI DALL-E 3 API.
//
// Agents use this to produce illustrations, diagrams, or visual assets
// as part of a report or creative pipeline. Images are saved to disk and
// the file path is returned so downstream agents can reference them.
//
// API key: https://platform.openai.com
// Cost: ~$0.04 per standard image, ~$0.08 per HD image
//
// agents.yaml:
//
//	tools:
//	  - name: "generate_image"
//	    api_key: "env:OPENAI_API_KEY"
//	    base_dir: "./outputs/images"   # optional: where to save images
type GenerateImageTool struct {
	client        *http.Client
	apiKey        string
	baseDir       string
	openAiBaseURL string
}

type generateImageInput struct {
	Prompt  string `json:"prompt"`
	Size    string `json:"size,omitempty"`    // "1024x1024", "1792x1024", "1024x1792"
	Quality string `json:"quality,omitempty"` // "standard" or "hd"
	Style   string `json:"style,omitempty"`   // "vivid" or "natural"
	SaveAs  string `json:"save_as,omitempty"` // filename on disk, e.g. "cover.png"
}

type generateImageOutput struct {
	FilePath      string `json:"file_path"`
	Prompt        string `json:"prompt"`
	RevisedPrompt string `json:"revised_prompt"` // DALL-E 3 sometimes rewrites the prompt
	Size          string `json:"size"`
	Quality       string `json:"quality"`
}

// dalleRequest mirrors the OpenAI images/generations request body.
type dalleRequest struct {
	Model          string `json:"model"`
	Prompt         string `json:"prompt"`
	N              int    `json:"n"`
	Size           string `json:"size"`
	Quality        string `json:"quality"`
	Style          string `json:"style"`
	ResponseFormat string `json:"response_format"` // always "b64_json" so we save without a second request
}

// dalleResponse mirrors the OpenAI images/generations response body.
type dalleResponse struct {
	Data []struct {
		B64JSON       string `json:"b64_json"`
		RevisedPrompt string `json:"revised_prompt"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// GenerateImage returns a ready-to-use GenerateImageTool.
// apiKey is the OpenAI API key.
// baseDir is where generated images are saved — defaults to "./outputs/images".
func GenerateImage(apiKey, baseDir string) *GenerateImageTool {
	if baseDir == "" {
		baseDir = "./outputs/images"
	}
	return &GenerateImageTool{
		client:        &http.Client{Timeout: 60 * time.Second}, // generation can take 10-30s
		apiKey:        apiKey,
		baseDir:       baseDir,
		openAiBaseURL: "https://api.openai.com",
	}
}

// Name returns the tool identifier.
// This satisfies the tools.Tool interface.
func (t *GenerateImageTool) Name() string {
	return "generate_image"
}

// Schema describes this tool to the LLM.
// This satisfies the tools.Tool interface.
func (t *GenerateImageTool) Schema() tools.Schema {
	return tools.Schema{
		Description: "Generate an image from a text description using DALL-E 3. " +
			"Use to create illustrations, diagrams, cover images, or any visual content. " +
			"The image is saved to disk and the file path is returned. " +
			"Be specific and descriptive for best results.",
		Parameters: map[string]tools.Parameter{
			"prompt": {
				Type: "string",
				Description: "Detailed description of the image to generate. " +
					"Be specific about style, content, colours, and composition. " +
					"Example: 'A clean technical diagram showing three connected boxes " +
					"labelled Planner, Writer, and Critic with arrows between them, " +
					"minimal flat design, white background'",
				Required: true,
			},
			"size": {
				Type: "string",
				Description: "Image dimensions. Options: '1024x1024' (square, default), " +
					"'1792x1024' (landscape), '1024x1792' (portrait).",
				Required: false,
			},
			"quality": {
				Type:        "string",
				Description: "'standard' (default, faster and cheaper) or 'hd' (more detail).",
				Required:    false,
			},
			"style": {
				Type:        "string",
				Description: "'vivid' (default, dramatic) or 'natural' (more realistic).",
				Required:    false,
			},
			"save_as": {
				Type:        "string",
				Description: "Filename to save the image as. Example: 'cover.png', 'diagram.jpg'. Defaults to a timestamp name.",
				Required:    false,
			},
		},
	}
}

// Execute generates an image and saves it to disk when the LLM requests it.
// This satisfies the tools.Tool interface.
func (t *GenerateImageTool) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	// — parse the LLM's input
	var params generateImageInput
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("generate_image: invalid input: %w", err)
	}
	if strings.TrimSpace(params.Prompt) == "" {
		return nil, fmt.Errorf("generate_image: prompt is required")
	}

	// Apply defaults
	size := params.Size
	if size == "" {
		size = "1024x1024"
	}
	quality := params.Quality
	if quality == "" {
		quality = "standard"
	}
	style := params.Style
	if style == "" {
		style = "vivid"
	}

	// Validate size — DALL-E 3 only accepts these three
	switch size {
	case "1024x1024", "1792x1024", "1024x1792":
	default:
		return nil, fmt.Errorf(
			"generate_image: invalid size %q — valid options: 1024x1024, 1792x1024, 1024x1792",
			size,
		)
	}

	// — build the request
	// We request b64_json so we can save the image without a second HTTP call
	dalleReq := dalleRequest{
		Model:          "dall-e-3",
		Prompt:         params.Prompt,
		N:              1,
		Size:           size,
		Quality:        quality,
		Style:          style,
		ResponseFormat: "b64_json",
	}

	bodyBytes, err := json.Marshal(dalleReq)
	if err != nil {
		return nil, fmt.Errorf("generate_image: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		t.openAiBaseURL+"/v1/images/generations",
		bytes.NewReader(bodyBytes),
	)
	if err != nil {
		return nil, fmt.Errorf("generate_image: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+t.apiKey)

	// — make the call
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("generate_image: request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("generate_image: read response: %w", err)
	}

	// — handle errors
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return nil, fmt.Errorf("generate_image: invalid API key — check OPENAI_API_KEY")
	case http.StatusTooManyRequests:
		return nil, fmt.Errorf("generate_image: rate limit exceeded — try again later")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("generate_image: api returned status %d: %s", resp.StatusCode, respBody)
	}

	// — parse the response
	var apiResp dalleResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("generate_image: parse response: %w", err)
	}
	if apiResp.Error != nil {
		return nil, fmt.Errorf("generate_image: api error: %s", apiResp.Error.Message)
	}
	if len(apiResp.Data) == 0 || apiResp.Data[0].B64JSON == "" {
		return nil, fmt.Errorf("generate_image: no image data returned")
	}

	imageData := apiResp.Data[0]

	// — decode base64 and save to disk
	filePath, err := t.saveImage(imageData.B64JSON, params.SaveAs)
	if err != nil {
		return nil, fmt.Errorf("generate_image: save image: %w", err)
	}

	return json.Marshal(generateImageOutput{
		FilePath:      filePath,
		Prompt:        params.Prompt,
		RevisedPrompt: imageData.RevisedPrompt,
		Size:          size,
		Quality:       quality,
	})
}

// saveImage decodes the base64 image data and writes it to disk inside baseDir.
// Returns the full file path of the saved image.
func (t *GenerateImageTool) saveImage(b64Data, saveAs string) (string, error) {
	// Decode the base64-encoded PNG data
	imageBytes, err := base64.StdEncoding.DecodeString(b64Data)
	if err != nil {
		return "", fmt.Errorf("decode base64: %w", err)
	}

	// Build a safe filename
	if saveAs == "" {
		saveAs = fmt.Sprintf("image_%d.png", time.Now().UnixMilli())
	}

	// Path traversal protection — same rules as write_file
	cleanName := filepath.Clean(saveAs)
	if filepath.IsAbs(cleanName) {
		return "", fmt.Errorf("save_as must be a relative filename, not an absolute path")
	}
	if strings.Contains(cleanName, "..") {
		return "", fmt.Errorf("save_as must not contain '..'")
	}

	// Ensure the full save path stays inside baseDir
	absBase, err := filepath.Abs(t.baseDir)
	if err != nil {
		return "", fmt.Errorf("resolve base dir: %w", err)
	}

	fullPath := filepath.Join(t.baseDir, cleanName)
	absFull, err := filepath.Abs(fullPath)
	if err != nil {
		return "", fmt.Errorf("resolve save path: %w", err)
	}
	if !strings.HasPrefix(absFull, absBase+string(filepath.Separator)) && absFull != absBase {
		return "", fmt.Errorf("save_as %q resolves outside the allowed directory", saveAs)
	}

	// Create parent directories and write the file
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return "", fmt.Errorf("create directories: %w", err)
	}
	if err := os.WriteFile(fullPath, imageBytes, 0644); err != nil {
		return "", fmt.Errorf("write image file: %w", err)
	}

	return fullPath, nil
}

// init registers GenerateImageTool as a built-in.
func init() {
	tools.RegisterBuiltin("generate_image", func(cfg tools.ToolConfig) (tools.Tool, error) {
		if cfg.APIKey == "" {
			return nil, fmt.Errorf(
				"generate_image requires an api_key\n" +
					"  add to agents.yaml:  api_key: \"env:OPENAI_API_KEY\"\n" +
					"  then set the env:    export OPENAI_API_KEY=sk-...",
			)
		}
		return GenerateImage(cfg.APIKey, cfg.BaseDir), nil
	})
}

// compile-time interface check
var _ tools.Tool = (*GenerateImageTool)(nil)
