package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"groq-go/internal/tool"
)

type GlobTool struct{}

type GlobArgs struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path,omitempty"`
}

func NewGlobTool() *GlobTool {
	return &GlobTool{}
}

func (t *GlobTool) Name() string {
	return "Glob"
}

func (t *GlobTool) Description() string {
	return "Fast file pattern matching. Supports glob patterns like \"**/*.js\" or \"src/**/*.ts\". Returns matching file paths."
}

func (t *GlobTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pattern": map[string]any{
				"type":        "string",
				"description": "The glob pattern to match files against",
			},
			"path": map[string]any{
				"type":        "string",
				"description": "The directory to search in. Defaults to current working directory.",
			},
		},
		"required": []string{"pattern"},
	}
}

type fileInfo struct {
	path    string
	modTime int64
}

func (t *GlobTool) Execute(ctx context.Context, argsJSON json.RawMessage) (tool.Result, error) {
	var args GlobArgs
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return tool.NewErrorResult(fmt.Sprintf("invalid arguments: %v", err)), nil
	}

	if args.Pattern == "" {
		return tool.NewErrorResult("pattern is required"), nil
	}

	searchPath := args.Path
	if searchPath == "" {
		var err error
		searchPath, err = os.Getwd()
		if err != nil {
			return tool.NewErrorResult(fmt.Sprintf("failed to get working directory: %v", err)), nil
		}
	}

	// Make path absolute if not already
	if !filepath.IsAbs(searchPath) {
		cwd, err := os.Getwd()
		if err != nil {
			return tool.NewErrorResult(fmt.Sprintf("failed to get working directory: %v", err)), nil
		}
		searchPath = filepath.Join(cwd, searchPath)
	}

	pattern := filepath.Join(searchPath, args.Pattern)

	matches, err := doublestar.FilepathGlob(pattern)
	if err != nil {
		return tool.NewErrorResult(fmt.Sprintf("glob error: %v", err)), nil
	}

	if len(matches) == 0 {
		return tool.NewResult("No files matched the pattern"), nil
	}

	// Get file info for sorting by modification time
	var files []fileInfo
	for _, match := range matches {
		info, err := os.Stat(match)
		if err != nil {
			continue
		}
		if info.IsDir() {
			continue
		}
		files = append(files, fileInfo{
			path:    match,
			modTime: info.ModTime().Unix(),
		})
	}

	// Sort by modification time (newest first)
	sort.Slice(files, func(i, j int) bool {
		return files[i].modTime > files[j].modTime
	})

	// Limit results
	maxResults := 100
	if len(files) > maxResults {
		files = files[:maxResults]
	}

	var paths []string
	for _, f := range files {
		paths = append(paths, f.path)
	}

	result := strings.Join(paths, "\n")
	if len(files) == maxResults {
		result += fmt.Sprintf("\n\n(showing first %d results)", maxResults)
	}

	return tool.NewResult(result), nil
}
