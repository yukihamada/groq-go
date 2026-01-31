package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"groq-go/internal/tool"
)

type ReadTool struct{}

type ReadArgs struct {
	FilePath string `json:"file_path"`
	Offset   int    `json:"offset,omitempty"`
	Limit    int    `json:"limit,omitempty"`
}

func NewReadTool() *ReadTool {
	return &ReadTool{}
}

func (t *ReadTool) Name() string {
	return "Read"
}

func (t *ReadTool) Description() string {
	return "Reads a file from the filesystem. Returns the file content with line numbers."
}

func (t *ReadTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"file_path": map[string]any{
				"type":        "string",
				"description": "The absolute path to the file to read",
			},
			"offset": map[string]any{
				"type":        "integer",
				"description": "The line number to start reading from (1-indexed). Default is 1.",
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "The maximum number of lines to read. Default is 2000.",
			},
		},
		"required": []string{"file_path"},
	}
}

func (t *ReadTool) Execute(ctx context.Context, argsJSON json.RawMessage) (tool.Result, error) {
	var args ReadArgs
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return tool.NewErrorResult(fmt.Sprintf("invalid arguments: %v", err)), nil
	}

	if args.FilePath == "" {
		return tool.NewErrorResult("file_path is required"), nil
	}

	if args.Limit == 0 {
		args.Limit = 2000
	}
	if args.Offset == 0 {
		args.Offset = 1
	}

	file, err := os.Open(args.FilePath)
	if err != nil {
		return tool.NewErrorResult(fmt.Sprintf("failed to open file: %v", err)), nil
	}
	defer file.Close()

	var lines []string
	scanner := bufio.NewScanner(file)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		if lineNum < args.Offset {
			continue
		}
		if lineNum >= args.Offset+args.Limit {
			break
		}

		line := scanner.Text()
		// Truncate long lines
		if len(line) > 2000 {
			line = line[:2000] + "... (truncated)"
		}
		lines = append(lines, fmt.Sprintf("%6d\t%s", lineNum, line))
	}

	if err := scanner.Err(); err != nil {
		return tool.NewErrorResult(fmt.Sprintf("error reading file: %v", err)), nil
	}

	if len(lines) == 0 {
		return tool.NewResult("(empty file or no lines in range)"), nil
	}

	return tool.NewResult(strings.Join(lines, "\n")), nil
}
