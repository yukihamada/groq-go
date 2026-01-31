package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"groq-go/internal/tool"
)

type GitTool struct{}

type GitArgs struct {
	Command string `json:"command"`
	Args    string `json:"args,omitempty"`
	Message string `json:"message,omitempty"`
	Path    string `json:"path,omitempty"`
}

func NewGitTool() *GitTool {
	return &GitTool{}
}

func (t *GitTool) Name() string {
	return "Git"
}

func (t *GitTool) Description() string {
	return `Execute git commands. Available commands:
- status: Show working tree status
- diff: Show changes (use args for specific files)
- log: Show commit logs (default: last 10)
- add: Stage files (use args for file paths, or "." for all)
- commit: Create commit (use message parameter)
- push: Push to remote
- pull: Pull from remote
- branch: List or create branches (use args for branch name)
- checkout: Switch branches (use args for branch name)
- stash: Stash changes`
}

func (t *GitTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{
				"type":        "string",
				"enum":        []string{"status", "diff", "log", "add", "commit", "push", "pull", "branch", "checkout", "stash"},
				"description": "The git command to execute",
			},
			"args": map[string]any{
				"type":        "string",
				"description": "Additional arguments for the command",
			},
			"message": map[string]any{
				"type":        "string",
				"description": "Commit message (for commit command)",
			},
			"path": map[string]any{
				"type":        "string",
				"description": "Working directory path (defaults to current directory)",
			},
		},
		"required": []string{"command"},
	}
}

func (t *GitTool) Execute(ctx context.Context, argsJSON json.RawMessage) (tool.Result, error) {
	var args GitArgs
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return tool.NewErrorResult(fmt.Sprintf("invalid arguments: %v", err)), nil
	}

	if args.Command == "" {
		return tool.NewErrorResult("command is required"), nil
	}

	// Build git command
	var gitArgs []string

	switch args.Command {
	case "status":
		gitArgs = []string{"status", "--short"}

	case "diff":
		gitArgs = []string{"diff"}
		if args.Args != "" {
			gitArgs = append(gitArgs, strings.Fields(args.Args)...)
		}

	case "log":
		gitArgs = []string{"log", "--oneline", "-n", "10"}
		if args.Args != "" {
			gitArgs = append(gitArgs, strings.Fields(args.Args)...)
		}

	case "add":
		if args.Args == "" {
			return tool.NewErrorResult("args required for add command (e.g., '.' or file paths)"), nil
		}
		gitArgs = []string{"add"}
		gitArgs = append(gitArgs, strings.Fields(args.Args)...)

	case "commit":
		if args.Message == "" {
			return tool.NewErrorResult("message required for commit command"), nil
		}
		gitArgs = []string{"commit", "-m", args.Message}

	case "push":
		gitArgs = []string{"push"}
		if args.Args != "" {
			gitArgs = append(gitArgs, strings.Fields(args.Args)...)
		}

	case "pull":
		gitArgs = []string{"pull"}
		if args.Args != "" {
			gitArgs = append(gitArgs, strings.Fields(args.Args)...)
		}

	case "branch":
		if args.Args == "" {
			gitArgs = []string{"branch", "-a"}
		} else {
			gitArgs = []string{"branch", args.Args}
		}

	case "checkout":
		if args.Args == "" {
			return tool.NewErrorResult("args required for checkout command (branch name)"), nil
		}
		gitArgs = []string{"checkout", args.Args}

	case "stash":
		gitArgs = []string{"stash"}
		if args.Args != "" {
			gitArgs = append(gitArgs, strings.Fields(args.Args)...)
		}

	default:
		return tool.NewErrorResult(fmt.Sprintf("unknown command: %s", args.Command)), nil
	}

	// Execute git command
	cmd := exec.CommandContext(ctx, "git", gitArgs...)
	if args.Path != "" {
		cmd.Dir = args.Path
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	output := stdout.String()
	if stderr.Len() > 0 {
		if output != "" {
			output += "\n"
		}
		output += stderr.String()
	}

	if err != nil {
		return tool.NewErrorResult(fmt.Sprintf("git %s failed: %s\n%s", args.Command, err.Error(), output)), nil
	}

	if output == "" {
		output = fmt.Sprintf("git %s completed successfully", args.Command)
	}

	return tool.NewResult(output), nil
}
