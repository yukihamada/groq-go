package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// ServerConfig represents a single MCP server configuration
type ServerConfig struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

// Config represents the MCP configuration file
type Config struct {
	MCPServers map[string]ServerConfig `json:"mcpServers"`
}

// Manager manages multiple MCP server connections
type Manager struct {
	mu      sync.RWMutex
	clients map[string]*Client
	config  Config
}

// NewManager creates a new MCP manager
func NewManager() *Manager {
	return &Manager{
		clients: make(map[string]*Client),
	}
}

// LoadConfig loads MCP configuration from the config file
func (m *Manager) LoadConfig() error {
	configPath := m.getConfigPath()

	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			// No config file, that's ok
			return nil
		}
		return fmt.Errorf("failed to read config: %w", err)
	}

	if err := json.Unmarshal(data, &m.config); err != nil {
		return fmt.Errorf("failed to parse config: %w", err)
	}

	return nil
}

// StartServers starts all configured MCP servers
func (m *Manager) StartServers(ctx context.Context) error {
	for name, cfg := range m.config.MCPServers {
		if err := m.startServer(ctx, name, cfg); err != nil {
			// Log error but continue with other servers
			fmt.Fprintf(os.Stderr, "Warning: failed to start MCP server %s: %v\n", name, err)
			continue
		}
	}
	return nil
}

func (m *Manager) startServer(ctx context.Context, name string, cfg ServerConfig) error {
	// Convert env map to slice
	var env []string
	if len(cfg.Env) > 0 {
		env = os.Environ()
		for k, v := range cfg.Env {
			env = append(env, fmt.Sprintf("%s=%s", k, v))
		}
	}

	client, err := NewClient(name, cfg.Command, cfg.Args, env)
	if err != nil {
		return err
	}

	// Initialize the connection
	if err := client.Initialize(ctx); err != nil {
		client.Close()
		return err
	}

	// Get available tools
	if _, err := client.ListTools(ctx); err != nil {
		client.Close()
		return err
	}

	m.mu.Lock()
	m.clients[name] = client
	m.mu.Unlock()

	return nil
}

// GetAllTools returns all tools from all connected MCP servers
func (m *Manager) GetAllTools() map[string][]ToolDef {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string][]ToolDef)
	for name, client := range m.clients {
		result[name] = client.tools
	}
	return result
}

// CallTool calls a tool on the specified MCP server
func (m *Manager) CallTool(ctx context.Context, serverName, toolName string, args map[string]any) (*CallToolResult, error) {
	m.mu.RLock()
	client, ok := m.clients[serverName]
	m.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("MCP server %q not found", serverName)
	}

	return client.CallTool(ctx, toolName, args)
}

// FindToolServer finds which server provides a given tool
func (m *Manager) FindToolServer(toolName string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for serverName, client := range m.clients {
		for _, tool := range client.tools {
			if tool.Name == toolName {
				return serverName, true
			}
		}
	}
	return "", false
}

// Close shuts down all MCP servers
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, client := range m.clients {
		client.Close()
	}
	m.clients = make(map[string]*Client)
}

// ServerCount returns the number of connected servers
func (m *Manager) ServerCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.clients)
}

// ServerNames returns the names of all connected servers
func (m *Manager) ServerNames() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	names := make([]string, 0, len(m.clients))
	for name := range m.clients {
		names = append(names, name)
	}
	return names
}

func (m *Manager) getConfigPath() string {
	// Check for config in current directory first
	if _, err := os.Stat("mcp.json"); err == nil {
		return "mcp.json"
	}

	// Then check ~/.config/groq-go/mcp.json
	home, err := os.UserHomeDir()
	if err != nil {
		return "mcp.json"
	}

	return filepath.Join(home, ".config", "groq-go", "mcp.json")
}
