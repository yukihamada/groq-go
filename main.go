package main

import (
	"context"
	"fmt"
	"os"

	"groq-go/internal/client"
	"groq-go/internal/config"
	"groq-go/internal/mcp"
	"groq-go/internal/repl"
	"groq-go/internal/tool"
	"groq-go/internal/tool/tools"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	// Create API client
	apiClient := client.New(cfg.APIKey, client.WithModel(cfg.Model))

	// Create tool registry and register built-in tools
	registry := tool.NewRegistry()
	registerTools(registry)

	// Initialize MCP manager
	mcpManager := mcp.NewManager()
	defer mcpManager.Close()

	// Load and start MCP servers
	if err := mcpManager.LoadConfig(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to load MCP config: %v\n", err)
	} else {
		ctx := context.Background()
		if err := mcpManager.StartServers(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to start MCP servers: %v\n", err)
		}

		// Register MCP tools
		mcpToolCount := mcp.RegisterMCPTools(registry, mcpManager)
		if mcpToolCount > 0 {
			fmt.Fprintf(os.Stderr, "Loaded %d MCP tools from %d servers\n", mcpToolCount, mcpManager.ServerCount())
		}
	}

	// Create and run REPL
	r, err := repl.New(apiClient, registry)
	if err != nil {
		return err
	}

	return r.Run()
}

func registerTools(registry *tool.Registry) {
	registry.Register(tools.NewReadTool())
	registry.Register(tools.NewWriteTool())
	registry.Register(tools.NewEditTool())
	registry.Register(tools.NewGlobTool())
	registry.Register(tools.NewGrepTool())
	registry.Register(tools.NewBashTool())
	registry.Register(tools.NewWebFetchTool())
	registry.Register(tools.NewBrowserTool())
}
