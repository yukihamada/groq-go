package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"groq-go/internal/tool"
	"groq-go/internal/version"
)

// VersionTool allows the AI to manage agent versions
type VersionTool struct {
	manager *version.Manager
}

func NewVersionTool(manager *version.Manager) *VersionTool {
	return &VersionTool{manager: manager}
}

func (t *VersionTool) Name() string {
	return "Version"
}

func (t *VersionTool) Description() string {
	return `Manage agent versions for self-evolution.

## Actions
- "create": Create a new version (requires name, optional description)
- "list": List all versions
- "get": Get details of a version (requires id)
- "build": Build a version's binary (requires id)
- "start": Start a version on a new port (requires id)
- "stop": Stop a running version (requires id)
- "restart": Restart a version (requires id)
- "delete": Delete a version (requires id)
- "logs": Get version logs (requires id, optional lines)
- "apply_changes": Apply code changes to a version's branch (requires id, path, content)

## Workflow
1. Create a new version with "create"
2. Apply code changes with "apply_changes" (modifies files on the version's branch)
3. Build with "build"
4. Start with "start" to run on a different port
5. Users can switch to test the new version
6. If good, the version can be promoted to main

## Notes
- Each version runs on a different port (8081-8090)
- Max 5 versions allowed
- Users can switch between versions via the UI`
}

func (t *VersionTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"description": "Action to perform",
				"enum":        []string{"create", "list", "get", "build", "start", "stop", "restart", "delete", "logs", "apply_changes"},
			},
			"id": map[string]any{
				"type":        "string",
				"description": "Version ID (required for get, build, start, stop, restart, delete, logs, apply_changes)",
			},
			"name": map[string]any{
				"type":        "string",
				"description": "Version name (required for create)",
			},
			"description": map[string]any{
				"type":        "string",
				"description": "Version description (optional for create)",
			},
			"path": map[string]any{
				"type":        "string",
				"description": "File path for apply_changes",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "File content for apply_changes",
			},
			"lines": map[string]any{
				"type":        "integer",
				"description": "Number of log lines to return (default: 50)",
			},
		},
		"required": []string{"action"},
	}
}

func (t *VersionTool) Execute(ctx context.Context, args json.RawMessage) (tool.Result, error) {
	if t.manager == nil {
		return tool.Result{Content: "Version management not available", IsError: true}, nil
	}

	var params struct {
		Action      string `json:"action"`
		ID          string `json:"id"`
		Name        string `json:"name"`
		Description string `json:"description"`
		Path        string `json:"path"`
		Content     string `json:"content"`
		Lines       int    `json:"lines"`
	}

	if err := json.Unmarshal(args, &params); err != nil {
		return tool.Result{Content: err.Error(), IsError: true}, nil
	}

	switch params.Action {
	case "create":
		return t.handleCreate(ctx, params.Name, params.Description)

	case "list":
		return t.handleList()

	case "get":
		return t.handleGet(params.ID)

	case "build":
		return t.handleBuild(ctx, params.ID)

	case "start":
		return t.handleStart(ctx, params.ID)

	case "stop":
		return t.handleStop(ctx, params.ID)

	case "restart":
		return t.handleRestart(ctx, params.ID)

	case "delete":
		return t.handleDelete(ctx, params.ID)

	case "logs":
		lines := params.Lines
		if lines <= 0 {
			lines = 50
		}
		return t.handleLogs(params.ID, lines)

	case "apply_changes":
		return t.handleApplyChanges(ctx, params.ID, params.Path, params.Content)

	default:
		return tool.Result{Content: "Unknown action: " + params.Action, IsError: true}, nil
	}
}

func (t *VersionTool) handleCreate(ctx context.Context, name, description string) (tool.Result, error) {
	if name == "" {
		return tool.Result{Content: "name is required for create action", IsError: true}, nil
	}

	v, err := t.manager.CreateVersion(ctx, name, description)
	if err != nil {
		return tool.Result{Content: err.Error(), IsError: true}, nil
	}

	return tool.Result{Content: fmt.Sprintf("Created version: %s (ID: %s, Branch: %s)\nNext: Apply changes with 'apply_changes', then 'build' to compile.", v.Name, v.ID, v.Branch)}, nil
}

func (t *VersionTool) handleList() (tool.Result, error) {
	versions := t.manager.ListVersions()
	if len(versions) == 0 {
		return tool.Result{Content: "No versions created yet. Use 'create' to create a new version."}, nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Versions (%d):\n", len(versions)))
	for _, v := range versions {
		statusIcon := getStatusIcon(v.Status)
		portInfo := ""
		if v.Port > 0 {
			portInfo = fmt.Sprintf(" (port %d)", v.Port)
		}
		sb.WriteString(fmt.Sprintf("  %s %s [%s] - %s%s\n", statusIcon, v.ID, v.Status, v.Name, portInfo))
		if v.Description != "" {
			sb.WriteString(fmt.Sprintf("      %s\n", v.Description))
		}
	}
	return tool.Result{Content: sb.String()}, nil
}

func (t *VersionTool) handleGet(id string) (tool.Result, error) {
	if id == "" {
		return tool.Result{Content: "id is required for get action", IsError: true}, nil
	}

	v, ok := t.manager.GetVersion(id)
	if !ok {
		return tool.Result{Content: fmt.Sprintf("Version %s not found", id), IsError: true}, nil
	}

	data, _ := json.MarshalIndent(v, "", "  ")
	return tool.Result{Content: string(data)}, nil
}

func (t *VersionTool) handleBuild(ctx context.Context, id string) (tool.Result, error) {
	if id == "" {
		return tool.Result{Content: "id is required for build action", IsError: true}, nil
	}

	if err := t.manager.BuildVersion(ctx, id); err != nil {
		return tool.Result{Content: fmt.Sprintf("Build failed: %v", err), IsError: true}, nil
	}

	v, _ := t.manager.GetVersion(id)
	return tool.Result{Content: fmt.Sprintf("Build successful for version %s (%s)\nBinary: %s\nNext: Use 'start' to run the version.", v.Name, v.ID, v.BinaryPath)}, nil
}

func (t *VersionTool) handleStart(ctx context.Context, id string) (tool.Result, error) {
	if id == "" {
		return tool.Result{Content: "id is required for start action", IsError: true}, nil
	}

	if err := t.manager.StartVersion(ctx, id); err != nil {
		return tool.Result{Content: fmt.Sprintf("Start failed: %v", err), IsError: true}, nil
	}

	v, _ := t.manager.GetVersion(id)
	return tool.Result{Content: fmt.Sprintf("Started version %s (%s) on port %d\nAccess: http://localhost:%d\nUsers can switch to this version via the version selector in the UI.", v.Name, v.ID, v.Port, v.Port)}, nil
}

func (t *VersionTool) handleStop(ctx context.Context, id string) (tool.Result, error) {
	if id == "" {
		return tool.Result{Content: "id is required for stop action", IsError: true}, nil
	}

	if err := t.manager.StopVersion(ctx, id); err != nil {
		return tool.Result{Content: fmt.Sprintf("Stop failed: %v", err), IsError: true}, nil
	}

	return tool.Result{Content: fmt.Sprintf("Stopped version %s", id)}, nil
}

func (t *VersionTool) handleRestart(ctx context.Context, id string) (tool.Result, error) {
	if id == "" {
		return tool.Result{Content: "id is required for restart action", IsError: true}, nil
	}

	if err := t.manager.RestartVersion(ctx, id); err != nil {
		return tool.Result{Content: fmt.Sprintf("Restart failed: %v", err), IsError: true}, nil
	}

	v, _ := t.manager.GetVersion(id)
	return tool.Result{Content: fmt.Sprintf("Restarted version %s on port %d", v.Name, v.Port)}, nil
}

func (t *VersionTool) handleDelete(ctx context.Context, id string) (tool.Result, error) {
	if id == "" {
		return tool.Result{Content: "id is required for delete action", IsError: true}, nil
	}

	if err := t.manager.DeleteVersion(ctx, id); err != nil {
		return tool.Result{Content: fmt.Sprintf("Delete failed: %v", err), IsError: true}, nil
	}

	return tool.Result{Content: fmt.Sprintf("Deleted version %s", id)}, nil
}

func (t *VersionTool) handleLogs(id string, lines int) (tool.Result, error) {
	if id == "" {
		return tool.Result{Content: "id is required for logs action", IsError: true}, nil
	}

	logs, err := t.manager.GetVersionLogs(id, lines)
	if err != nil {
		return tool.Result{Content: fmt.Sprintf("Failed to get logs: %v", err), IsError: true}, nil
	}

	return tool.Result{Content: logs}, nil
}

func (t *VersionTool) handleApplyChanges(ctx context.Context, id, path, content string) (tool.Result, error) {
	if id == "" {
		return tool.Result{Content: "id is required for apply_changes action", IsError: true}, nil
	}
	if path == "" || content == "" {
		return tool.Result{Content: "path and content are required for apply_changes action", IsError: true}, nil
	}

	v, ok := t.manager.GetVersion(id)
	if !ok {
		return tool.Result{Content: fmt.Sprintf("Version %s not found", id), IsError: true}, nil
	}

	sim := t.manager.GetSelfImprove()
	if sim == nil {
		return tool.Result{Content: "Self-improve not available", IsError: true}, nil
	}

	// Checkout the version's branch
	repoDir := t.manager.GetRepoDir()
	if err := runGit(ctx, repoDir, "checkout", v.Branch); err != nil {
		return tool.Result{Content: fmt.Sprintf("Failed to checkout branch: %v", err), IsError: true}, nil
	}

	// Write the file
	if err := sim.WriteFile(ctx, path, content); err != nil {
		return tool.Result{Content: fmt.Sprintf("Failed to write file: %v", err), IsError: true}, nil
	}

	return tool.Result{Content: fmt.Sprintf("Applied changes to %s on branch %s\nNext: Use 'build' to compile the changes.", path, v.Branch)}, nil
}

func getStatusIcon(s version.Status) string {
	switch s {
	case version.StatusPending:
		return "⏳"
	case version.StatusBuilding:
		return "🔨"
	case version.StatusReady:
		return "✅"
	case version.StatusRunning:
		return "🟢"
	case version.StatusFailed:
		return "❌"
	case version.StatusStopped:
		return "⏹️"
	default:
		return "❓"
	}
}

// runGit is a helper to run git commands
func runGit(ctx context.Context, dir string, args ...string) error {
	return version.RunGitCmd(ctx, dir, args...)
}
