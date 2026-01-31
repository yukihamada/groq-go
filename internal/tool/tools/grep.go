package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"groq-go/internal/tool"
)

type GrepTool struct{}

type GrepArgs struct {
	Pattern    string `json:"pattern"`
	Path       string `json:"path,omitempty"`
	Glob       string `json:"glob,omitempty"`
	OutputMode string `json:"output_mode,omitempty"`
	Context    int    `json:"context,omitempty"`
	HeadLimit  int    `json:"head_limit,omitempty"`
}

func NewGrepTool() *GrepTool {
	return &GrepTool{}
}

func (t *GrepTool) Name() string {
	return "Grep"
}

func (t *GrepTool) Description() string {
	return "Search for patterns in files using regular expressions. Supports glob filters for file types."
}

func (t *GrepTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pattern": map[string]any{
				"type":        "string",
				"description": "The regular expression pattern to search for",
			},
			"path": map[string]any{
				"type":        "string",
				"description": "File or directory to search in. Defaults to current directory.",
			},
			"glob": map[string]any{
				"type":        "string",
				"description": "Glob pattern to filter files (e.g., \"*.go\", \"*.{ts,tsx}\")",
			},
			"output_mode": map[string]any{
				"type":        "string",
				"description": "Output mode: 'content' shows matching lines, 'files_with_matches' shows file paths only. Default is 'files_with_matches'.",
				"enum":        []string{"content", "files_with_matches"},
			},
			"context": map[string]any{
				"type":        "integer",
				"description": "Number of lines to show before and after each match (only for content mode)",
			},
			"head_limit": map[string]any{
				"type":        "integer",
				"description": "Limit output to first N matches",
			},
		},
		"required": []string{"pattern"},
	}
}

type grepMatch struct {
	file    string
	line    int
	content string
}

func (t *GrepTool) Execute(ctx context.Context, argsJSON json.RawMessage) (tool.Result, error) {
	var args GrepArgs
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return tool.NewErrorResult(fmt.Sprintf("invalid arguments: %v", err)), nil
	}

	if args.Pattern == "" {
		return tool.NewErrorResult("pattern is required"), nil
	}

	re, err := regexp.Compile(args.Pattern)
	if err != nil {
		return tool.NewErrorResult(fmt.Sprintf("invalid regex pattern: %v", err)), nil
	}

	searchPath := args.Path
	if searchPath == "" {
		searchPath, _ = os.Getwd()
	}

	if !filepath.IsAbs(searchPath) {
		cwd, _ := os.Getwd()
		searchPath = filepath.Join(cwd, searchPath)
	}

	outputMode := args.OutputMode
	if outputMode == "" {
		outputMode = "files_with_matches"
	}

	headLimit := args.HeadLimit
	if headLimit == 0 {
		headLimit = 100
	}

	// Collect files to search
	var files []string
	info, err := os.Stat(searchPath)
	if err != nil {
		return tool.NewErrorResult(fmt.Sprintf("path error: %v", err)), nil
	}

	if info.IsDir() {
		globPattern := "**/*"
		if args.Glob != "" {
			globPattern = "**/" + args.Glob
		}
		pattern := filepath.Join(searchPath, globPattern)
		matches, err := doublestar.FilepathGlob(pattern)
		if err != nil {
			return tool.NewErrorResult(fmt.Sprintf("glob error: %v", err)), nil
		}
		for _, m := range matches {
			info, err := os.Stat(m)
			if err == nil && !info.IsDir() {
				files = append(files, m)
			}
		}
	} else {
		files = []string{searchPath}
	}

	var matches []grepMatch
	matchedFiles := make(map[string]bool)
	matchCount := 0

	for _, file := range files {
		if matchCount >= headLimit {
			break
		}

		fileMatches, err := searchFile(file, re, args.Context)
		if err != nil {
			continue
		}

		for _, m := range fileMatches {
			if matchCount >= headLimit {
				break
			}
			matches = append(matches, m)
			matchedFiles[m.file] = true
			matchCount++
		}
	}

	if len(matches) == 0 {
		return tool.NewResult("No matches found"), nil
	}

	var result strings.Builder
	if outputMode == "files_with_matches" {
		for file := range matchedFiles {
			result.WriteString(file)
			result.WriteString("\n")
		}
	} else {
		currentFile := ""
		for _, m := range matches {
			if m.file != currentFile {
				if currentFile != "" {
					result.WriteString("\n")
				}
				result.WriteString(fmt.Sprintf("=== %s ===\n", m.file))
				currentFile = m.file
			}
			result.WriteString(fmt.Sprintf("%d: %s\n", m.line, m.content))
		}
	}

	return tool.NewResult(strings.TrimSpace(result.String())), nil
}

func searchFile(path string, re *regexp.Regexp, contextLines int) ([]grepMatch, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var matches []grepMatch
	var lines []string
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	for i, line := range lines {
		if re.MatchString(line) {
			if contextLines > 0 {
				start := i - contextLines
				if start < 0 {
					start = 0
				}
				end := i + contextLines + 1
				if end > len(lines) {
					end = len(lines)
				}
				for j := start; j < end; j++ {
					matches = append(matches, grepMatch{
						file:    path,
						line:    j + 1,
						content: lines[j],
					})
				}
			} else {
				matches = append(matches, grepMatch{
					file:    path,
					line:    i + 1,
					content: line,
				})
			}
		}
	}

	return matches, nil
}
