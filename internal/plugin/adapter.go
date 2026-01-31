package plugin

import (
	"context"
	"encoding/json"

	"groq-go/internal/tool"
)

// PluginToolAdapter wraps a plugin tool to implement the tool.Tool interface
type PluginToolAdapter struct {
	manager    *Manager
	pluginName string
	toolDef    PluginTool
}

// NewPluginToolAdapter creates a new adapter for a plugin tool
func NewPluginToolAdapter(manager *Manager, pluginName string, toolDef PluginTool) *PluginToolAdapter {
	return &PluginToolAdapter{
		manager:    manager,
		pluginName: pluginName,
		toolDef:    toolDef,
	}
}

// Name returns the tool name prefixed with plugin name
func (a *PluginToolAdapter) Name() string {
	return a.pluginName + "_" + a.toolDef.Name
}

// Description returns the tool description
func (a *PluginToolAdapter) Description() string {
	return a.toolDef.Description + " (Plugin: " + a.pluginName + ")"
}

// Parameters returns the tool parameters
func (a *PluginToolAdapter) Parameters() map[string]any {
	if a.toolDef.Parameters == nil {
		return map[string]any{
			"type":       "object",
			"properties": map[string]any{},
			"required":   []string{},
		}
	}
	return a.toolDef.Parameters
}

// Execute executes the plugin tool
func (a *PluginToolAdapter) Execute(ctx context.Context, args json.RawMessage) (tool.Result, error) {
	resp, err := a.manager.ExecuteTool(ctx, a.pluginName, a.toolDef.Name, args)
	if err != nil {
		return tool.Result{Content: err.Error(), IsError: true}, nil
	}

	return tool.Result{Content: resp.Content, IsError: resp.IsError}, nil
}

// RegisterPluginTools registers all enabled plugin tools with the registry
func RegisterPluginTools(registry *tool.Registry, manager *Manager) int {
	if manager == nil {
		return 0
	}

	enabledTools := manager.GetEnabledTools()
	count := 0

	for _, pt := range enabledTools {
		adapter := NewPluginToolAdapter(manager, pt.PluginName, pt.Tool)
		if err := registry.Register(adapter); err == nil {
			count++
		}
	}

	return count
}
