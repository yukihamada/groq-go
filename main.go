package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"groq-go/internal/client"
	"groq-go/internal/config"
	"groq-go/internal/knowledge"
	"groq-go/internal/mcp"
	"groq-go/internal/plugin"
	"groq-go/internal/repl"
	"groq-go/internal/selfimprove"
	"groq-go/internal/tool"
	"groq-go/internal/tool/tools"
	"groq-go/internal/web"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// Parse flags
	webMode := flag.Bool("web", false, "Start web server instead of CLI")
	webAddr := flag.String("addr", ":8080", "Web server address")
	flag.Parse()

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	// Create API client with provider keys
	opts := []client.Option{client.WithModel(cfg.Model)}
	if cfg.MoonshotKey != "" {
		opts = append(opts, client.WithProviderKey("moonshot", cfg.MoonshotKey))
	}
	if cfg.OpenAIKey != "" {
		opts = append(opts, client.WithProviderKey("openai", cfg.OpenAIKey))
	}
	apiClient := client.New(cfg.APIKey, opts...)

	// Initialize knowledge base
	kb, err := knowledge.NewKnowledgeBase(knowledge.DefaultKnowledgeDir())
	if err != nil {
		log.Printf("Warning: failed to initialize knowledge base: %v", err)
	}

	// Initialize self-improvement manager
	var selfImproveManager *selfimprove.Manager
	if os.Getenv("GITHUB_TOKEN") != "" {
		selfImproveManager, err = selfimprove.NewManager()
		if err != nil {
			log.Printf("Warning: failed to initialize self-improve manager: %v", err)
		} else {
			// Initialize repo in background
			go func() {
				ctx := context.Background()
				if err := selfImproveManager.Init(ctx); err != nil {
					log.Printf("Warning: failed to init self-improve repo: %v", err)
				} else {
					log.Printf("Self-improvement repo initialized at %s", selfImproveManager.GetRepoDir())
				}
			}()
		}
	}

	// Create tool registry and register built-in tools
	registry := tool.NewRegistry()
	registerTools(registry, kb, selfImproveManager)

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

	// Initialize plugin manager
	pluginManager, err := plugin.NewManager()
	if err != nil {
		log.Printf("Warning: failed to initialize plugin manager: %v", err)
	} else {
		// Register plugin tools
		pluginToolCount := plugin.RegisterPluginTools(registry, pluginManager)
		if pluginToolCount > 0 {
			fmt.Fprintf(os.Stderr, "Loaded %d plugin tools\n", pluginToolCount)
		}
	}

	// Start in web mode or CLI mode
	if *webMode {
		server := web.NewServer(apiClient, registry, kb, pluginManager, *webAddr)
		return server.Start()
	}

	// Create and run REPL
	r, err := repl.New(apiClient, registry)
	if err != nil {
		return err
	}

	return r.Run()
}

func registerTools(registry *tool.Registry, kb *knowledge.KnowledgeBase, sim *selfimprove.Manager) {
	registry.Register(tools.NewReadTool())
	registry.Register(tools.NewWriteTool())
	registry.Register(tools.NewEditTool())
	registry.Register(tools.NewGlobTool())
	registry.Register(tools.NewGrepTool())
	registry.Register(tools.NewBashTool())
	registry.Register(tools.NewWebFetchTool())
	registry.Register(tools.NewBrowserTool())
	registry.Register(tools.NewGitTool())
	registry.Register(tools.NewImageGenTool())
	registry.Register(tools.NewCodeExecTool())

	// Knowledge base tools
	if kb != nil {
		registry.Register(tools.NewKnowledgeSearchTool(kb))
		registry.Register(tools.NewKnowledgeListTool(kb))
	}

	// Self-improvement tool
	if sim != nil {
		registry.Register(tools.NewSelfImproveTool(sim))
	}
}
