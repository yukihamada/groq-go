package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"groq-go/internal/selfimprove"
	"groq-go/internal/tool"
)

// SelfImproveTool allows the AI to modify its own source code
type SelfImproveTool struct {
	manager *selfimprove.Manager
}

func NewSelfImproveTool(manager *selfimprove.Manager) *SelfImproveTool {
	return &SelfImproveTool{manager: manager}
}

func (t *SelfImproveTool) Name() string {
	return "SelfImprove"
}

func (t *SelfImproveTool) Description() string {
	return `Modify the groq-go source code to improve this AI system.

## Basic Actions
- "list": List source files (use pattern to filter)
- "read": Read a source file
- "write": Write/modify a source file
- "status": Show git status
- "diff": Show uncommitted changes
- "commit": Commit changes with a message
- "history": Show commit history

## Safe Deployment
- "verify_build": Test if code compiles (ALWAYS do this before pushing!)
- "safe_push": Push only if build succeeds + mark as known good
- "mark_good": Mark current deployed version as known good

## Rollback Options (in order of preference)
- "rollback": Rollback to previous commit
- "rollback_to": Rollback to specific commit (use "hash" parameter)
- "rollback_safe": Rollback to last known good commit
- "fly_rollback": Get Fly.io rollback instructions (last resort)

## Safety Protocol
1. Make changes with "write"
2. Check with "diff"
3. Verify with "verify_build"
4. Commit with "commit"
5. Deploy with "safe_push" (NOT "push")
6. If broken: "rollback_safe" or "fly_rollback"`
}

func (t *SelfImproveTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"description": "Action to perform",
				"enum":        []string{"list", "read", "write", "status", "diff", "commit", "push", "safe_push", "verify_build", "mark_good", "rollback", "rollback_to", "rollback_safe", "fly_rollback", "history"},
			},
			"path": map[string]any{
				"type":        "string",
				"description": "File path (relative to repo root) for read/write actions",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "File content for write action",
			},
			"message": map[string]any{
				"type":        "string",
				"description": "Commit message for commit action",
			},
			"pattern": map[string]any{
				"type":        "string",
				"description": "Filter pattern for list action (e.g., '.go', 'internal/')",
			},
			"hash": map[string]any{
				"type":        "string",
				"description": "Commit hash for rollback_to action",
			},
		},
		"required": []string{"action"},
	}
}

func (t *SelfImproveTool) Execute(ctx context.Context, args json.RawMessage) (tool.Result, error) {
	if t.manager == nil {
		return tool.Result{Content: "Self-improvement not available (GITHUB_TOKEN not set)", IsError: true}, nil
	}

	var params struct {
		Action  string `json:"action"`
		Path    string `json:"path"`
		Content string `json:"content"`
		Message string `json:"message"`
		Pattern string `json:"pattern"`
		Hash    string `json:"hash"`
	}

	if err := json.Unmarshal(args, &params); err != nil {
		return tool.Result{Content: err.Error(), IsError: true}, nil
	}

	switch params.Action {
	case "list":
		files, err := t.manager.ListFiles(ctx, params.Pattern)
		if err != nil {
			return tool.Result{Content: err.Error(), IsError: true}, nil
		}
		return tool.Result{Content: fmt.Sprintf("Files (%d):\n%s", len(files), strings.Join(files, "\n"))}, nil

	case "read":
		if params.Path == "" {
			return tool.Result{Content: "path is required for read action", IsError: true}, nil
		}
		content, err := t.manager.ReadFile(ctx, params.Path)
		if err != nil {
			return tool.Result{Content: err.Error(), IsError: true}, nil
		}
		return tool.Result{Content: content}, nil

	case "write":
		if params.Path == "" || params.Content == "" {
			return tool.Result{Content: "path and content are required for write action", IsError: true}, nil
		}
		if err := t.manager.WriteFile(ctx, params.Path, params.Content); err != nil {
			return tool.Result{Content: err.Error(), IsError: true}, nil
		}
		return tool.Result{Content: fmt.Sprintf("Successfully wrote to %s", params.Path)}, nil

	case "status":
		status, err := t.manager.GetStatus(ctx)
		if err != nil {
			return tool.Result{Content: err.Error(), IsError: true}, nil
		}
		return tool.Result{Content: status}, nil

	case "diff":
		diff, err := t.manager.GetDiff(ctx)
		if err != nil {
			return tool.Result{Content: err.Error(), IsError: true}, nil
		}
		if diff == "" {
			return tool.Result{Content: "No changes"}, nil
		}
		return tool.Result{Content: diff}, nil

	case "commit":
		if params.Message == "" {
			return tool.Result{Content: "message is required for commit action", IsError: true}, nil
		}
		commit, err := t.manager.Commit(ctx, params.Message)
		if err != nil {
			return tool.Result{Content: err.Error(), IsError: true}, nil
		}
		return tool.Result{Content: fmt.Sprintf("Committed: %s - %s", commit.Hash[:8], commit.Message)}, nil

	case "verify_build":
		if err := t.manager.VerifyBuild(ctx); err != nil {
			return tool.Result{Content: fmt.Sprintf("❌ Build failed: %v", err), IsError: true}, nil
		}
		return tool.Result{Content: "✅ Build verification passed. Safe to push."}, nil

	case "push":
		if err := t.manager.Push(ctx); err != nil {
			return tool.Result{Content: err.Error(), IsError: true}, nil
		}
		return tool.Result{Content: "⚠️ Pushed to GitHub (without build verification). Consider using 'safe_push' instead."}, nil

	case "safe_push":
		if err := t.manager.SafePush(ctx); err != nil {
			return tool.Result{Content: fmt.Sprintf("❌ Safe push failed: %v", err), IsError: true}, nil
		}
		return tool.Result{Content: "✅ Build verified and pushed to GitHub. Marked as known good. Auto-deploy will start shortly. Check https://groq-go-yuki.fly.dev/ in 2-3 minutes."}, nil

	case "mark_good":
		if err := t.manager.MarkAsGood(ctx); err != nil {
			return tool.Result{Content: err.Error(), IsError: true}, nil
		}
		return tool.Result{Content: fmt.Sprintf("✅ Current commit marked as known good: %s", t.manager.GetLastKnownGood())}, nil

	case "rollback":
		if err := t.manager.RollbackToLast(ctx); err != nil {
			return tool.Result{Content: err.Error(), IsError: true}, nil
		}
		return tool.Result{Content: "Rolled back to previous version. Use 'verify_build', 'commit', and 'safe_push' to deploy the rollback."}, nil

	case "rollback_to":
		if params.Hash == "" {
			return tool.Result{Content: "hash is required for rollback_to action", IsError: true}, nil
		}
		if err := t.manager.RollbackToCommit(ctx, params.Hash); err != nil {
			return tool.Result{Content: err.Error(), IsError: true}, nil
		}
		return tool.Result{Content: fmt.Sprintf("Rolled back to commit %s. Use 'verify_build', 'commit', and 'safe_push' to deploy.", params.Hash)}, nil

	case "rollback_safe":
		lastGood := t.manager.GetLastKnownGood()
		if lastGood == "" {
			return tool.Result{Content: "No known good commit saved. Use 'fly_rollback' for Fly.io manual rollback.", IsError: true}, nil
		}
		if err := t.manager.RollbackToSafe(ctx); err != nil {
			return tool.Result{Content: err.Error(), IsError: true}, nil
		}
		return tool.Result{Content: fmt.Sprintf("✅ Rolled back to last known good: %s. Use 'commit' and 'safe_push' to deploy.", lastGood)}, nil

	case "fly_rollback":
		info, err := t.manager.GetFlyRollbackInfo(ctx)
		if err != nil {
			return tool.Result{Content: err.Error(), IsError: true}, nil
		}
		return tool.Result{Content: info}, nil

	case "history":
		history := t.manager.GetHistory()
		if len(history) == 0 {
			return tool.Result{Content: "No commit history"}, nil
		}
		var sb strings.Builder
		sb.WriteString("Commit History:\n")
		lastGood := t.manager.GetLastKnownGood()
		for i, c := range history {
			marker := ""
			if lastGood != "" && strings.HasPrefix(c.Hash, lastGood[:8]) {
				marker = " ✅ (known good)"
			}
			sb.WriteString(fmt.Sprintf("%d. %s - %s%s\n", i+1, c.Hash[:8], c.Message, marker))
		}
		if lastGood != "" {
			sb.WriteString(fmt.Sprintf("\nLast known good: %s\n", lastGood[:8]))
		}
		return tool.Result{Content: sb.String()}, nil

	default:
		return tool.Result{Content: "Unknown action: " + params.Action, IsError: true}, nil
	}
}
