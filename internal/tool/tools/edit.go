package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"groq-go/internal/tool"
)

type EditTool struct{}

type EditArgs struct {
	FilePath   string `json:"file_path"`
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all,omitempty"`
}

func NewEditTool() *EditTool {
	return &EditTool{}
}

func (t *EditTool) Name() string {
	return "Edit"
}

func (t *EditTool) Description() string {
	return "Performs exact string replacements in files. The old_string must match exactly."
}

func (t *EditTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"file_path": map[string]any{
				"type":        "string",
				"description": "The absolute path to the file to modify",
			},
			"old_string": map[string]any{
				"type":        "string",
				"description": "The exact text to replace",
			},
			"new_string": map[string]any{
				"type":        "string",
				"description": "The text to replace it with",
			},
			"replace_all": map[string]any{
				"type":        "boolean",
				"description": "Replace all occurrences (default false)",
			},
		},
		"required": []string{"file_path", "old_string", "new_string"},
	}
}

func (t *EditTool) Execute(ctx context.Context, argsJSON json.RawMessage) (tool.Result, error) {
	var args EditArgs
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return tool.NewErrorResult(fmt.Sprintf("invalid arguments: %v", err)), nil
	}

	if args.FilePath == "" {
		return tool.NewErrorResult("file_path is required"), nil
	}
	if args.OldString == "" {
		return tool.NewErrorResult("old_string is required"), nil
	}
	if args.OldString == args.NewString {
		return tool.NewErrorResult("old_string and new_string must be different"), nil
	}

	content, err := os.ReadFile(args.FilePath)
	if err != nil {
		return tool.NewErrorResult(fmt.Sprintf("failed to read file: %v", err)), nil
	}

	contentStr := string(content)
	count := strings.Count(contentStr, args.OldString)

	if count == 0 {
		return tool.NewErrorResult("old_string not found in file"), nil
	}

	if count > 1 && !args.ReplaceAll {
		return tool.NewErrorResult(fmt.Sprintf("old_string found %d times. Use replace_all=true to replace all, or provide a more specific string", count)), nil
	}

	var newContent string
	if args.ReplaceAll {
		newContent = strings.ReplaceAll(contentStr, args.OldString, args.NewString)
	} else {
		newContent = strings.Replace(contentStr, args.OldString, args.NewString, 1)
	}

	if err := os.WriteFile(args.FilePath, []byte(newContent), 0644); err != nil {
		return tool.NewErrorResult(fmt.Sprintf("failed to write file: %v", err)), nil
	}

	if args.ReplaceAll {
		return tool.NewResult(fmt.Sprintf("Successfully replaced %d occurrences in %s", count, args.FilePath)), nil
	}
	return tool.NewResult(fmt.Sprintf("Successfully edited %s", args.FilePath)), nil
}
