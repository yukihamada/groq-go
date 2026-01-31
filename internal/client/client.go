package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	DefaultBaseURL = "https://api.groq.com/openai/v1"
	DefaultModel   = "llama-3.3-70b-versatile"
	DefaultTimeout = 120 * time.Second

	// Provider base URLs
	GroqBaseURL     = "https://api.groq.com/openai/v1"
	MoonshotBaseURL = "https://api.moonshot.cn/v1"
	OpenAIBaseURL   = "https://api.openai.com/v1"
)

// Client is the API client supporting multiple providers
type Client struct {
	baseURL      string
	apiKey       string
	model        string
	httpClient   *http.Client
	providerKeys map[string]string // provider -> apiKey
}

// Option is a function that configures the client
type Option func(*Client)

// WithBaseURL sets a custom base URL
func WithBaseURL(url string) Option {
	return func(c *Client) {
		c.baseURL = url
	}
}

// WithModel sets the default model
func WithModel(model string) Option {
	return func(c *Client) {
		c.model = model
	}
}

// WithHTTPClient sets a custom HTTP client
func WithHTTPClient(httpClient *http.Client) Option {
	return func(c *Client) {
		c.httpClient = httpClient
	}
}

// WithProviderKey sets an API key for a specific provider
func WithProviderKey(provider, apiKey string) Option {
	return func(c *Client) {
		if c.providerKeys == nil {
			c.providerKeys = make(map[string]string)
		}
		c.providerKeys[provider] = apiKey
	}
}

// New creates a new API client
func New(apiKey string, opts ...Option) *Client {
	c := &Client{
		baseURL: DefaultBaseURL,
		apiKey:  apiKey,
		model:   DefaultModel,
		httpClient: &http.Client{
			Timeout: DefaultTimeout,
		},
		providerKeys: make(map[string]string),
	}
	// Default Groq key
	c.providerKeys["groq"] = apiKey

	for _, opt := range opts {
		opt(c)
	}
	return c
}

// getProviderConfig returns baseURL and apiKey for the current model
func (c *Client) getProviderConfig() (baseURL, apiKey string) {
	switch {
	case isKimiModel(c.model):
		return MoonshotBaseURL, c.providerKeys["moonshot"]
	case isOpenAIModel(c.model):
		return OpenAIBaseURL, c.providerKeys["openai"]
	default:
		return GroqBaseURL, c.providerKeys["groq"]
	}
}

func isKimiModel(model string) bool {
	// Kimi K2 is available on Groq, so return false
	// Only use Moonshot API for moonshot-specific models
	switch model {
	case "moonshot-v1-8k", "moonshot-v1-32k", "moonshot-v1-128k":
		return true
	}
	return false
}

func isOpenAIModel(model string) bool {
	switch model {
	case "gpt-4", "gpt-4-turbo", "gpt-4o", "gpt-4o-mini", "gpt-3.5-turbo":
		return true
	}
	return false
}

// Model returns the current model
func (c *Client) Model() string {
	return c.model
}

// SetModel changes the model
func (c *Client) SetModel(model string) {
	c.model = model
}

// ChatCompletion sends a non-streaming chat completion request
func (c *Client) ChatCompletion(ctx context.Context, messages []Message, tools []Tool) (*ChatCompletionResponse, error) {
	baseURL, apiKey := c.getProviderConfig()
	if apiKey == "" {
		return nil, fmt.Errorf("no API key configured for model %s", c.model)
	}

	req := ChatCompletionRequest{
		Model:    c.model,
		Messages: messages,
		Tools:    tools,
		Stream:   false,
	}

	if len(tools) > 0 {
		req.ToolChoice = "auto"
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var errResp ErrorResponse
		if err := json.Unmarshal(respBody, &errResp); err == nil {
			return nil, fmt.Errorf("API error: %s (%s)", errResp.Error.Message, errResp.Error.Type)
		}
		return nil, fmt.Errorf("API error: status %d, body: %s", resp.StatusCode, string(respBody))
	}

	var result ChatCompletionResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &result, nil
}

// ChatCompletionStream sends a streaming chat completion request
func (c *Client) ChatCompletionStream(ctx context.Context, messages []Message, tools []Tool) (*StreamReader, error) {
	baseURL, apiKey := c.getProviderConfig()
	if apiKey == "" {
		return nil, fmt.Errorf("no API key configured for model %s", c.model)
	}

	req := ChatCompletionRequest{
		Model:    c.model,
		Messages: messages,
		Tools:    tools,
		Stream:   true,
	}

	if len(tools) > 0 {
		req.ToolChoice = "auto"
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(resp.Body)
		var errResp ErrorResponse
		if err := json.Unmarshal(respBody, &errResp); err == nil {
			return nil, fmt.Errorf("API error: %s (%s)", errResp.Error.Message, errResp.Error.Type)
		}
		return nil, fmt.Errorf("API error: status %d, body: %s", resp.StatusCode, string(respBody))
	}

	return NewStreamReader(resp.Body), nil
}
