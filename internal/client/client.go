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
	GroqBaseURL      = "https://api.groq.com/openai/v1"
	MoonshotBaseURL  = "https://api.moonshot.cn/v1"
	OpenAIBaseURL    = "https://api.openai.com/v1"
	AnthropicBaseURL = "https://api.anthropic.com/v1"
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
	case isClaudeModel(c.model):
		return AnthropicBaseURL, c.providerKeys["anthropic"]
	case isKimiModel(c.model):
		return MoonshotBaseURL, c.providerKeys["moonshot"]
	case isOpenAIModel(c.model):
		return OpenAIBaseURL, c.providerKeys["openai"]
	default:
		return GroqBaseURL, c.providerKeys["groq"]
	}
}

func isClaudeModel(model string) bool {
	switch model {
	case "claude-3-opus-20240229", "claude-3-sonnet-20240229", "claude-3-haiku-20240307",
		"claude-3-5-sonnet-20240620", "claude-3-5-sonnet-20241022", "claude-3-5-haiku-20241022",
		"claude-sonnet-4-20250514", "claude-opus-4-20250514":
		return true
	}
	return false
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
	if isClaudeModel(c.model) {
		return c.claudeChatCompletion(ctx, messages, tools)
	}

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

// claudeChatCompletion handles Claude API requests
func (c *Client) claudeChatCompletion(ctx context.Context, messages []Message, tools []Tool) (*ChatCompletionResponse, error) {
	apiKey := c.providerKeys["anthropic"]
	if apiKey == "" {
		return nil, fmt.Errorf("no API key configured for Claude (set ANTHROPIC_API_KEY)")
	}

	// Convert messages to Claude format
	claudeReq := c.buildClaudeRequest(messages, tools, false)

	body, err := json.Marshal(claudeReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", AnthropicBaseURL+"/messages", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

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
		return nil, fmt.Errorf("Claude API error: status %d, body: %s", resp.StatusCode, string(respBody))
	}

	// Parse Claude response and convert to OpenAI format
	return c.parseClaudeResponse(respBody)
}

// ClaudeRequest represents Claude API request format
type ClaudeRequest struct {
	Model     string         `json:"model"`
	MaxTokens int            `json:"max_tokens"`
	System    string         `json:"system,omitempty"`
	Messages  []ClaudeMsg    `json:"messages"`
	Tools     []ClaudeTool   `json:"tools,omitempty"`
	Stream    bool           `json:"stream,omitempty"`
}

// ClaudeMsg represents a Claude message
type ClaudeMsg struct {
	Role    string        `json:"role"`
	Content []ClaudeBlock `json:"content"`
}

// ClaudeBlock represents content block in Claude message
type ClaudeBlock struct {
	Type      string `json:"type"`
	Text      string `json:"text,omitempty"`
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	Input     any    `json:"input,omitempty"`
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
}

// ClaudeTool represents a Claude tool
type ClaudeTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"input_schema"`
}

// ClaudeResponse represents Claude API response
type ClaudeResponse struct {
	ID           string        `json:"id"`
	Type         string        `json:"type"`
	Role         string        `json:"role"`
	Content      []ClaudeBlock `json:"content"`
	Model        string        `json:"model"`
	StopReason   string        `json:"stop_reason"`
	StopSequence string        `json:"stop_sequence"`
	Usage        struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// getMessageContent extracts string content from a Message
func getMessageContent(msg Message) string {
	if s, ok := msg.Content.(string); ok {
		return s
	}
	return ""
}

func (c *Client) buildClaudeRequest(messages []Message, tools []Tool, stream bool) ClaudeRequest {
	req := ClaudeRequest{
		Model:     c.model,
		MaxTokens: 4096,
		Stream:    stream,
	}

	// Extract system message
	var claudeMsgs []ClaudeMsg
	for _, msg := range messages {
		content := getMessageContent(msg)

		if msg.Role == "system" {
			req.System = content
			continue
		}

		// Handle tool results
		if msg.Role == "tool" {
			claudeMsgs = append(claudeMsgs, ClaudeMsg{
				Role: "user",
				Content: []ClaudeBlock{{
					Type:      "tool_result",
					ToolUseID: msg.ToolCallID,
					Content:   content,
				}},
			})
			continue
		}

		// Handle assistant messages with tool calls
		if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
			blocks := []ClaudeBlock{}
			if content != "" {
				blocks = append(blocks, ClaudeBlock{Type: "text", Text: content})
			}
			for _, tc := range msg.ToolCalls {
				blocks = append(blocks, ClaudeBlock{
					Type:  "tool_use",
					ID:    tc.ID,
					Name:  tc.Function.Name,
					Input: json.RawMessage(tc.Function.Arguments),
				})
			}
			claudeMsgs = append(claudeMsgs, ClaudeMsg{Role: "assistant", Content: blocks})
			continue
		}

		// Regular messages
		claudeMsgs = append(claudeMsgs, ClaudeMsg{
			Role:    msg.Role,
			Content: []ClaudeBlock{{Type: "text", Text: content}},
		})
	}
	req.Messages = claudeMsgs

	// Convert tools
	for _, t := range tools {
		req.Tools = append(req.Tools, ClaudeTool{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			InputSchema: t.Function.Parameters,
		})
	}

	return req
}

func (c *Client) parseClaudeResponse(body []byte) (*ChatCompletionResponse, error) {
	var claudeResp ClaudeResponse
	if err := json.Unmarshal(body, &claudeResp); err != nil {
		return nil, fmt.Errorf("failed to parse Claude response: %w", err)
	}

	// Convert to OpenAI format
	resp := &ChatCompletionResponse{
		ID:    claudeResp.ID,
		Model: claudeResp.Model,
		Usage: Usage{
			PromptTokens:     claudeResp.Usage.InputTokens,
			CompletionTokens: claudeResp.Usage.OutputTokens,
			TotalTokens:      claudeResp.Usage.InputTokens + claudeResp.Usage.OutputTokens,
		},
	}

	choice := Choice{
		Index:        0,
		FinishReason: claudeResp.StopReason,
	}

	// Extract text and tool calls
	var textParts []string
	var toolCalls []ToolCall
	for _, block := range claudeResp.Content {
		switch block.Type {
		case "text":
			textParts = append(textParts, block.Text)
		case "tool_use":
			inputJSON, _ := json.Marshal(block.Input)
			toolCalls = append(toolCalls, ToolCall{
				ID:   block.ID,
				Type: "function",
				Function: FunctionCall{
					Name:      block.Name,
					Arguments: string(inputJSON),
				},
			})
		}
	}

	choice.Message.Role = "assistant"
	choice.Message.Content = joinStrings(textParts)
	choice.Message.ToolCalls = toolCalls

	resp.Choices = []Choice{choice}
	return resp, nil
}

func joinStrings(parts []string) string {
	result := ""
	for i, p := range parts {
		if i > 0 {
			result += "\n"
		}
		result += p
	}
	return result
}

// ChatCompletionStream sends a streaming chat completion request
func (c *Client) ChatCompletionStream(ctx context.Context, messages []Message, tools []Tool) (*StreamReader, error) {
	if isClaudeModel(c.model) {
		return c.claudeChatCompletionStream(ctx, messages, tools)
	}

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

// claudeChatCompletionStream handles Claude streaming API requests
func (c *Client) claudeChatCompletionStream(ctx context.Context, messages []Message, tools []Tool) (*StreamReader, error) {
	apiKey := c.providerKeys["anthropic"]
	if apiKey == "" {
		return nil, fmt.Errorf("no API key configured for Claude (set ANTHROPIC_API_KEY)")
	}

	claudeReq := c.buildClaudeRequest(messages, tools, true)

	body, err := json.Marshal(claudeReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", AnthropicBaseURL+"/messages", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Claude API error: status %d, body: %s", resp.StatusCode, string(respBody))
	}

	return NewClaudeStreamReader(resp.Body), nil
}
