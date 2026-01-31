package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"groq-go/internal/tool"
)

// ToolAdapter wraps an MCP tool to implement the tool.Tool interface
type ToolAdapter struct {
	manager    *Manager
	serverName string
	toolDef    ToolDef
}

// NewToolAdapter creates a new tool adapter
func NewToolAdapter(manager *Manager, serverName string, toolDef ToolDef) *ToolAdapter {
	return &ToolAdapter{
		manager:    manager,
		serverName: serverName,
		toolDef:    toolDef,
	}
}

// Name returns the tool name with server prefix
func (t *ToolAdapter) Name() string {
	return fmt.Sprintf("mcp_%s_%s", t.serverName, t.toolDef.Name)
}

// Description returns the tool description
func (t *ToolAdapter) Description() string {
	desc := t.toolDef.Description
	if desc == "" {
		desc = fmt.Sprintf("MCP tool from %s server", t.serverName)
	}
	return fmt.Sprintf("[MCP:%s] %s", t.serverName, desc)
}

// Parameters returns the tool's JSON schema
func (t *ToolAdapter) Parameters() map[string]any {
	if t.toolDef.InputSchema != nil {
		return t.toolDef.InputSchema
	}
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

// Execute calls the MCP tool
func (t *ToolAdapter) Execute(ctx context.Context, argsJSON json.RawMessage) (tool.Result, error) {
	var args map[string]any
	if len(argsJSON) > 0 {
		if err := json.Unmarshal(argsJSON, &args); err != nil {
			return tool.NewErrorResult(fmt.Sprintf("invalid arguments: %v", err)), nil
		}
	}

	result, err := t.manager.CallTool(ctx, t.serverName, t.toolDef.Name, args)
	if err != nil {
		return tool.NewErrorResult(fmt.Sprintf("MCP call failed: %v", err)), nil
	}

	// Extract text content from result
	var content strings.Builder
	for _, block := range result.Content {
		if block.Type == "text" {
			if content.Len() > 0 {
				content.WriteString("\n")
			}
			content.WriteString(block.Text)
		}
	}

	if result.IsError {
		return tool.NewErrorResult(content.String()), nil
	}

	return tool.NewResult(content.String()), nil
}

// RegisterMCPTools registers all MCP tools with the tool registry
func RegisterMCPTools(registry *tool.Registry, manager *Manager) int {
	count := 0
	allTools := manager.GetAllTools()

	for serverName, tools := range allTools {
		for _, toolDef := range tools {
			adapter := NewToolAdapter(manager, serverName, toolDef)
			if err := registry.Register(adapter); err != nil {
				// Tool might already exist, skip it
				continue
			}
			count++
		}
	}

	return count
}
