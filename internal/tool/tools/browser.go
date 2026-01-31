package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"groq-go/internal/tool"
)

type BrowserTool struct{}

type BrowserArgs struct {
	URL        string `json:"url"`
	Action     string `json:"action"`
	Selector   string `json:"selector,omitempty"`
	OutputPath string `json:"output_path,omitempty"`
}

func NewBrowserTool() *BrowserTool {
	return &BrowserTool{}
}

func (t *BrowserTool) Name() string {
	return "Browser"
}

func (t *BrowserTool) Description() string {
	return "Control a browser using Playwright. Can take screenshots, get page content with JavaScript rendering, or interact with elements."
}

func (t *BrowserTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url": map[string]any{
				"type":        "string",
				"description": "The URL to navigate to",
			},
			"action": map[string]any{
				"type":        "string",
				"description": "Action to perform: 'screenshot', 'content', 'pdf'",
				"enum":        []string{"screenshot", "content", "pdf"},
			},
			"selector": map[string]any{
				"type":        "string",
				"description": "CSS selector for screenshot of specific element",
			},
			"output_path": map[string]any{
				"type":        "string",
				"description": "Output file path for screenshot/pdf (default: /tmp/browser_output.*)",
			},
		},
		"required": []string{"url", "action"},
	}
}

func (t *BrowserTool) Execute(ctx context.Context, argsJSON json.RawMessage) (tool.Result, error) {
	var args BrowserArgs
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return tool.NewErrorResult(fmt.Sprintf("invalid arguments: %v", err)), nil
	}

	if args.URL == "" {
		return tool.NewErrorResult("url is required"), nil
	}
	if args.Action == "" {
		return tool.NewErrorResult("action is required"), nil
	}

	// Check if npx is available
	if _, err := exec.LookPath("npx"); err != nil {
		return tool.NewErrorResult("npx not found. Please install Node.js to use the Browser tool."), nil
	}

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	switch args.Action {
	case "screenshot":
		return t.screenshot(ctx, args)
	case "content":
		return t.getContent(ctx, args)
	case "pdf":
		return t.pdf(ctx, args)
	default:
		return tool.NewErrorResult(fmt.Sprintf("unknown action: %s", args.Action)), nil
	}
}

func (t *BrowserTool) screenshot(ctx context.Context, args BrowserArgs) (tool.Result, error) {
	outputPath := args.OutputPath
	if outputPath == "" {
		outputPath = fmt.Sprintf("/tmp/screenshot_%d.png", time.Now().Unix())
	}

	cmdArgs := []string{"-y", "playwright", "screenshot", args.URL, outputPath}
	if args.Selector != "" {
		cmdArgs = append(cmdArgs, "--selector", args.Selector)
	}

	cmd := exec.CommandContext(ctx, "npx", cmdArgs...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return tool.NewErrorResult(fmt.Sprintf("screenshot failed: %v\n%s", err, stderr.String())), nil
	}

	return tool.NewResult(fmt.Sprintf("Screenshot saved to: %s", outputPath)), nil
}

func (t *BrowserTool) getContent(ctx context.Context, args BrowserArgs) (tool.Result, error) {
	// Use a Node.js script to get rendered content
	script := fmt.Sprintf(`
const { chromium } = require('playwright');
(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage();
  await page.goto('%s', { waitUntil: 'networkidle' });
  const content = await page.evaluate(() => document.body.innerText);
  console.log(content);
  await browser.close();
})();
`, strings.ReplaceAll(args.URL, "'", "\\'"))

	cmd := exec.CommandContext(ctx, "npx", "-y", "-p", "playwright", "node", "-e", script)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// Fallback to simple fetch if playwright fails
		return tool.NewErrorResult(fmt.Sprintf("content fetch failed: %v\n%s\nTry using WebFetch instead for simple pages.", err, stderr.String())), nil
	}

	content := stdout.String()
	if len(content) > 50000 {
		content = content[:50000] + "\n... (truncated)"
	}

	return tool.NewResult(content), nil
}

func (t *BrowserTool) pdf(ctx context.Context, args BrowserArgs) (tool.Result, error) {
	outputPath := args.OutputPath
	if outputPath == "" {
		outputPath = fmt.Sprintf("/tmp/page_%d.pdf", time.Now().Unix())
	}

	cmd := exec.CommandContext(ctx, "npx", "-y", "playwright", "pdf", args.URL, outputPath)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return tool.NewErrorResult(fmt.Sprintf("pdf generation failed: %v\n%s", err, stderr.String())), nil
	}

	return tool.NewResult(fmt.Sprintf("PDF saved to: %s", outputPath)), nil
}
