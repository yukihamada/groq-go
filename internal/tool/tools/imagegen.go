package tools

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
	"time"

	"groq-go/internal/tool"
)

type ImageGenTool struct {
	client *http.Client
}

type ImageGenArgs struct {
	Prompt   string `json:"prompt"`
	Style    string `json:"style,omitempty"`
	Width    int    `json:"width,omitempty"`
	Height   int    `json:"height,omitempty"`
	OutputPath string `json:"output_path,omitempty"`
}

func NewImageGenTool() *ImageGenTool {
	return &ImageGenTool{
		client: &http.Client{Timeout: 60 * time.Second},
	}
}

func (t *ImageGenTool) Name() string {
	return "ImageGen"
}

func (t *ImageGenTool) Description() string {
	return `Generate images from text prompts using Stability AI or OpenAI DALL-E.

Requires STABILITY_API_KEY or OPENAI_API_KEY environment variable.

Example prompts:
- "A futuristic city at sunset, cyberpunk style"
- "A cute cat wearing a hat, watercolor painting"
- "Abstract geometric patterns in blue and gold"`
}

func (t *ImageGenTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"prompt": map[string]any{
				"type":        "string",
				"description": "Text description of the image to generate",
			},
			"style": map[string]any{
				"type":        "string",
				"description": "Style preset (e.g., 'photographic', 'digital-art', 'anime')",
			},
			"width": map[string]any{
				"type":        "integer",
				"description": "Image width (default: 1024)",
			},
			"height": map[string]any{
				"type":        "integer",
				"description": "Image height (default: 1024)",
			},
			"output_path": map[string]any{
				"type":        "string",
				"description": "Path to save the image (default: auto-generated)",
			},
		},
		"required": []string{"prompt"},
	}
}

func (t *ImageGenTool) Execute(ctx context.Context, argsJSON json.RawMessage) (tool.Result, error) {
	var args ImageGenArgs
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return tool.NewErrorResult(fmt.Sprintf("invalid arguments: %v", err)), nil
	}

	if args.Prompt == "" {
		return tool.NewErrorResult("prompt is required"), nil
	}

	// Set defaults
	if args.Width == 0 {
		args.Width = 1024
	}
	if args.Height == 0 {
		args.Height = 1024
	}

	// Try Stability AI first, then OpenAI
	stabilityKey := os.Getenv("STABILITY_API_KEY")
	openaiKey := os.Getenv("OPENAI_API_KEY")

	var imageData []byte
	var err error

	if stabilityKey != "" {
		imageData, err = t.generateWithStability(ctx, stabilityKey, args)
	} else if openaiKey != "" {
		imageData, err = t.generateWithOpenAI(ctx, openaiKey, args)
	} else {
		return tool.NewErrorResult("No API key found. Set STABILITY_API_KEY or OPENAI_API_KEY"), nil
	}

	if err != nil {
		return tool.NewErrorResult(fmt.Sprintf("Image generation failed: %v", err)), nil
	}

	// Determine output path
	outputPath := args.OutputPath
	if outputPath == "" {
		home, _ := os.UserHomeDir()
		outputDir := filepath.Join(home, ".config", "groq-go", "images")
		os.MkdirAll(outputDir, 0755)
		outputPath = filepath.Join(outputDir, fmt.Sprintf("image_%d.png", time.Now().UnixNano()))
	}

	// Save image
	if err := os.WriteFile(outputPath, imageData, 0644); err != nil {
		return tool.NewErrorResult(fmt.Sprintf("Failed to save image: %v", err)), nil
	}

	return tool.NewResult(fmt.Sprintf("Image generated and saved to: %s", outputPath)), nil
}

func (t *ImageGenTool) generateWithStability(ctx context.Context, apiKey string, args ImageGenArgs) ([]byte, error) {
	// Use Stability AI's text-to-image endpoint
	url := "https://api.stability.ai/v1/generation/stable-diffusion-xl-1024-v1-0/text-to-image"

	reqBody := map[string]any{
		"text_prompts": []map[string]any{
			{"text": args.Prompt, "weight": 1},
		},
		"cfg_scale": 7,
		"width":     args.Width,
		"height":    args.Height,
		"samples":   1,
		"steps":     30,
	}

	if args.Style != "" {
		reqBody["style_preset"] = args.Style
	}

	bodyBytes, _ := json.Marshal(reqBody)

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Artifacts []struct {
			Base64 string `json:"base64"`
		} `json:"artifacts"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	if len(result.Artifacts) == 0 {
		return nil, fmt.Errorf("no image generated")
	}

	return base64.StdEncoding.DecodeString(result.Artifacts[0].Base64)
}

func (t *ImageGenTool) generateWithOpenAI(ctx context.Context, apiKey string, args ImageGenArgs) ([]byte, error) {
	// Use OpenAI's DALL-E endpoint
	url := "https://api.openai.com/v1/images/generations"

	// DALL-E 3 only supports certain sizes
	size := "1024x1024"
	if args.Width >= 1792 || args.Height >= 1792 {
		size = "1792x1024"
	}

	reqBody := map[string]any{
		"model":           "dall-e-3",
		"prompt":          args.Prompt,
		"n":               1,
		"size":            size,
		"response_format": "b64_json",
	}

	if args.Style != "" {
		reqBody["style"] = args.Style
	}

	bodyBytes, _ := json.Marshal(reqBody)

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Data []struct {
			B64JSON string `json:"b64_json"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	if len(result.Data) == 0 {
		return nil, fmt.Errorf("no image generated")
	}

	return base64.StdEncoding.DecodeString(result.Data[0].B64JSON)
}
