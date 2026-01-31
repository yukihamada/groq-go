package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"groq-go/internal/tool"
)

type WriteTool struct{}

type WriteArgs struct {
	FilePath string `json:"file_path"`
	Content  string `json:"content"`
}

func NewWriteTool() *WriteTool {
	return &WriteTool{}
}

func (t *WriteTool) Name() string {
	return "Write"
}

func (t *WriteTool) Description() string {
	return "Writes content to a file. Creates the file if it doesn't exist, overwrites if it does."
}

func (t *WriteTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"file_path": map[string]any{
				"type":        "string",
				"description": "The absolute path to the file to write",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "The content to write to the file",
			},
		},
		"required": []string{"file_path", "content"},
	}
}

func (t *WriteTool) Execute(ctx context.Context, argsJSON json.RawMessage) (tool.Result, error) {
	var args WriteArgs
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return tool.NewErrorResult(fmt.Sprintf("invalid arguments: %v", err)), nil
	}

	if args.FilePath == "" {
		return tool.NewErrorResult("file_path is required"), nil
	}

	// Create directory if it doesn't exist
	dir := filepath.Dir(args.FilePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return tool.NewErrorResult(fmt.Sprintf("failed to create directory: %v", err)), nil
	}

	if err := os.WriteFile(args.FilePath, []byte(args.Content), 0644); err != nil {
		return tool.NewErrorResult(fmt.Sprintf("failed to write file: %v", err)), nil
	}

	return tool.NewResult(fmt.Sprintf("Successfully wrote %d bytes to %s", len(args.Content), args.FilePath)), nil
}
