package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// Plugin represents a plugin configuration
type Plugin struct {
	Name        string            `json:"name" yaml:"name"`
	Description string            `json:"description" yaml:"description"`
	URL         string            `json:"url" yaml:"url"`
	Enabled     bool              `json:"enabled" yaml:"enabled"`
	Tools       []PluginTool      `json:"tools" yaml:"tools"`
	Headers     map[string]string `json:"headers,omitempty" yaml:"headers,omitempty"`
}

// PluginTool represents a tool exposed by a plugin
type PluginTool struct {
	Name        string         `json:"name" yaml:"name"`
	Description string         `json:"description" yaml:"description"`
	Parameters  map[string]any `json:"parameters" yaml:"parameters"`
}

// PluginResponse represents the response from a plugin tool execution
type PluginResponse struct {
	Content string `json:"content"`
	IsError bool   `json:"is_error"`
}

// Manager manages plugins
type Manager struct {
	plugins    map[string]*Plugin
	configPath string
	httpClient *http.Client
	mu         sync.RWMutex
}

// NewManager creates a new plugin manager
func NewManager() (*Manager, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}

	configPath := filepath.Join(home, ".config", "groq-go", "plugins.yaml")

	m := &Manager{
		plugins:    make(map[string]*Plugin),
		configPath: configPath,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}

	// Load existing config
	if err := m.loadConfig(); err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	return m, nil
}

// loadConfig loads plugins from config file
func (m *Manager) loadConfig() error {
	data, err := os.ReadFile(m.configPath)
	if err != nil {
		return err
	}

	var config struct {
		Plugins []Plugin `yaml:"plugins"`
	}

	if err := yaml.Unmarshal(data, &config); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, p := range config.Plugins {
		plugin := p
		m.plugins[plugin.Name] = &plugin
	}

	return nil
}

// saveConfig saves plugins to config file
func (m *Manager) saveConfig() error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var plugins []Plugin
	for _, p := range m.plugins {
		plugins = append(plugins, *p)
	}

	config := struct {
		Plugins []Plugin `yaml:"plugins"`
	}{Plugins: plugins}

	data, err := yaml.Marshal(&config)
	if err != nil {
		return err
	}

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(m.configPath), 0755); err != nil {
		return err
	}

	return os.WriteFile(m.configPath, data, 0644)
}

// AddPlugin adds a new plugin
func (m *Manager) AddPlugin(plugin *Plugin) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Discover tools from plugin
	if plugin.URL != "" && len(plugin.Tools) == 0 {
		tools, err := m.discoverTools(plugin)
		if err != nil {
			return fmt.Errorf("failed to discover tools: %w", err)
		}
		plugin.Tools = tools
	}

	m.plugins[plugin.Name] = plugin
	return m.saveConfig()
}

// RemovePlugin removes a plugin
func (m *Manager) RemovePlugin(name string) error {
	m.mu.Lock()
	delete(m.plugins, name)
	m.mu.Unlock()

	return m.saveConfig()
}

// GetPlugin returns a plugin by name
func (m *Manager) GetPlugin(name string) (*Plugin, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	p, ok := m.plugins[name]
	return p, ok
}

// ListPlugins returns all plugins
func (m *Manager) ListPlugins() []*Plugin {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var plugins []*Plugin
	for _, p := range m.plugins {
		plugins = append(plugins, p)
	}
	return plugins
}

// EnablePlugin enables a plugin
func (m *Manager) EnablePlugin(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	p, ok := m.plugins[name]
	if !ok {
		return fmt.Errorf("plugin not found: %s", name)
	}

	p.Enabled = true
	return m.saveConfig()
}

// DisablePlugin disables a plugin
func (m *Manager) DisablePlugin(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	p, ok := m.plugins[name]
	if !ok {
		return fmt.Errorf("plugin not found: %s", name)
	}

	p.Enabled = false
	return m.saveConfig()
}

// discoverTools calls the plugin's discovery endpoint to get available tools
func (m *Manager) discoverTools(plugin *Plugin) ([]PluginTool, error) {
	req, err := http.NewRequest("GET", plugin.URL+"/tools", nil)
	if err != nil {
		return nil, err
	}

	for k, v := range plugin.Headers {
		req.Header.Set(k, v)
	}

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("discovery failed: %s", string(body))
	}

	var tools []PluginTool
	if err := json.NewDecoder(resp.Body).Decode(&tools); err != nil {
		return nil, err
	}

	return tools, nil
}

// ExecuteTool executes a plugin tool
func (m *Manager) ExecuteTool(ctx context.Context, pluginName, toolName string, args json.RawMessage) (*PluginResponse, error) {
	m.mu.RLock()
	plugin, ok := m.plugins[pluginName]
	m.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("plugin not found: %s", pluginName)
	}

	if !plugin.Enabled {
		return nil, fmt.Errorf("plugin is disabled: %s", pluginName)
	}

	// Call the plugin's execute endpoint
	payload, _ := json.Marshal(map[string]any{
		"tool": toolName,
		"args": json.RawMessage(args),
	})

	req, err := http.NewRequestWithContext(ctx, "POST", plugin.URL+"/execute", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	for k, v := range plugin.Headers {
		req.Header.Set(k, v)
	}

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result PluginResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return &result, nil
}

// GetEnabledTools returns all enabled plugin tools
func (m *Manager) GetEnabledTools() []struct {
	PluginName string
	Tool       PluginTool
} {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []struct {
		PluginName string
		Tool       PluginTool
	}

	for _, p := range m.plugins {
		if !p.Enabled {
			continue
		}
		for _, t := range p.Tools {
			result = append(result, struct {
				PluginName string
				Tool       PluginTool
			}{
				PluginName: p.Name,
				Tool:       t,
			})
		}
	}

	return result
}

// DefaultPluginsDir returns the default plugins directory
func DefaultPluginsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".config", "groq-go", "plugins")
}
