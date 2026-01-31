package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"groq-go/internal/tool"
)

type WebFetchTool struct {
	client *http.Client
}

type WebFetchArgs struct {
	URL     string `json:"url"`
	Method  string `json:"method,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

func NewWebFetchTool() *WebFetchTool {
	return &WebFetchTool{
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (t *WebFetchTool) Name() string {
	return "WebFetch"
}

func (t *WebFetchTool) Description() string {
	return "Fetches content from a URL. Returns the response body. HTML is converted to readable text."
}

func (t *WebFetchTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url": map[string]any{
				"type":        "string",
				"description": "The URL to fetch",
			},
			"method": map[string]any{
				"type":        "string",
				"description": "HTTP method (GET, POST, etc.). Default is GET.",
				"enum":        []string{"GET", "POST", "PUT", "DELETE"},
			},
			"headers": map[string]any{
				"type":        "object",
				"description": "Optional HTTP headers",
			},
		},
		"required": []string{"url"},
	}
}

func (t *WebFetchTool) Execute(ctx context.Context, argsJSON json.RawMessage) (tool.Result, error) {
	var args WebFetchArgs
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return tool.NewErrorResult(fmt.Sprintf("invalid arguments: %v", err)), nil
	}

	if args.URL == "" {
		return tool.NewErrorResult("url is required"), nil
	}

	method := args.Method
	if method == "" {
		method = "GET"
	}

	req, err := http.NewRequestWithContext(ctx, method, args.URL, nil)
	if err != nil {
		return tool.NewErrorResult(fmt.Sprintf("failed to create request: %v", err)), nil
	}

	// Set default headers
	req.Header.Set("User-Agent", "groq-go/1.0")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

	// Add custom headers
	for k, v := range args.Headers {
		req.Header.Set(k, v)
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return tool.NewErrorResult(fmt.Sprintf("request failed: %v", err)), nil
	}
	defer resp.Body.Close()

	// Limit response size
	limitedReader := io.LimitReader(resp.Body, 100*1024) // 100KB limit
	body, err := io.ReadAll(limitedReader)
	if err != nil {
		return tool.NewErrorResult(fmt.Sprintf("failed to read response: %v", err)), nil
	}

	content := string(body)

	// Convert HTML to text if needed
	contentType := resp.Header.Get("Content-Type")
	if strings.Contains(contentType, "text/html") {
		content = htmlToText(content)
	}

	// Truncate if too long
	if len(content) > 50000 {
		content = content[:50000] + "\n... (truncated)"
	}

	result := fmt.Sprintf("Status: %d\nURL: %s\n\n%s", resp.StatusCode, resp.Request.URL.String(), content)
	return tool.NewResult(result), nil
}

// htmlToText converts HTML to readable plain text
func htmlToText(html string) string {
	// Remove script and style tags
	scriptRe := regexp.MustCompile(`(?is)<script.*?</script>`)
	html = scriptRe.ReplaceAllString(html, "")

	styleRe := regexp.MustCompile(`(?is)<style.*?</style>`)
	html = styleRe.ReplaceAllString(html, "")

	// Remove HTML comments
	commentRe := regexp.MustCompile(`(?s)<!--.*?-->`)
	html = commentRe.ReplaceAllString(html, "")

	// Convert common tags to text
	html = regexp.MustCompile(`(?i)<br\s*/?>|</?p>|</?div>|</?li>`).ReplaceAllString(html, "\n")
	html = regexp.MustCompile(`(?i)</?h[1-6]>`).ReplaceAllString(html, "\n\n")

	// Extract link text with URL
	linkRe := regexp.MustCompile(`(?i)<a[^>]*href=["']([^"']*)["'][^>]*>([^<]*)</a>`)
	html = linkRe.ReplaceAllString(html, "$2 ($1)")

	// Remove remaining tags
	tagRe := regexp.MustCompile(`<[^>]+>`)
	html = tagRe.ReplaceAllString(html, "")

	// Decode common HTML entities
	html = strings.ReplaceAll(html, "&nbsp;", " ")
	html = strings.ReplaceAll(html, "&amp;", "&")
	html = strings.ReplaceAll(html, "&lt;", "<")
	html = strings.ReplaceAll(html, "&gt;", ">")
	html = strings.ReplaceAll(html, "&quot;", "\"")
	html = strings.ReplaceAll(html, "&#39;", "'")

	// Clean up whitespace
	spaceRe := regexp.MustCompile(`[ \t]+`)
	html = spaceRe.ReplaceAllString(html, " ")

	// Clean up newlines
	newlineRe := regexp.MustCompile(`\n\s*\n\s*\n+`)
	html = newlineRe.ReplaceAllString(html, "\n\n")

	return strings.TrimSpace(html)
}
