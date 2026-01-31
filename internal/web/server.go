package web

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"

	"groq-go/internal/client"
	"groq-go/internal/tool"
)

//go:embed static/*
var staticFiles embed.FS

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins for development
	},
}

// Server represents the web server
type Server struct {
	client   *client.Client
	registry *tool.Registry
	executor *tool.Executor
	addr     string
}

// NewServer creates a new web server
func NewServer(c *client.Client, registry *tool.Registry, addr string) *Server {
	return &Server{
		client:   c,
		registry: registry,
		executor: tool.NewExecutor(registry),
		addr:     addr,
	}
}

// Start starts the web server
func (s *Server) Start() error {
	mux := http.NewServeMux()

	// Serve static files with proper headers
	staticFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		return err
	}
	fileServer := http.FileServer(http.FS(staticFS))
	mux.Handle("/", addSecurityHeaders(fileServer))

	// WebSocket endpoint
	mux.HandleFunc("/ws", s.handleWebSocket)

	// API endpoints
	mux.HandleFunc("/api/models", s.handleModels)

	log.Printf("Starting web server at http://localhost%s", s.addr)
	return http.ListenAndServe(s.addr, mux)
}

// WSMessage represents WebSocket message types
type WSMessage struct {
	Type    string `json:"type"`
	Content string `json:"content,omitempty"`
	Tool    string `json:"tool,omitempty"`
	Args    string `json:"args,omitempty"`
	Result  string `json:"result,omitempty"`
	Error   string `json:"error,omitempty"`
	Model   string `json:"model,omitempty"`
}

// Store for tracking tool call args
type toolCallInfo struct {
	name string
	args string
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade error: %v", err)
		return
	}
	defer conn.Close()

	// Send welcome message
	s.sendMessage(conn, WSMessage{
		Type:    "system",
		Content: fmt.Sprintf("Connected to groq-go. Model: %s", s.client.Model()),
	})

	// Message history for this session
	var history []client.Message
	history = append(history, client.Message{
		Role:    "system",
		Content: s.getSystemPrompt(),
	})

	var mu sync.Mutex

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("WebSocket error: %v", err)
			}
			break
		}

		var msg WSMessage
		if err := json.Unmarshal(message, &msg); err != nil {
			s.sendMessage(conn, WSMessage{Type: "error", Error: "Invalid message format"})
			continue
		}

		switch msg.Type {
		case "chat":
			mu.Lock()
			s.handleChat(conn, msg.Content, &history)
			mu.Unlock()

		case "model":
			if msg.Model != "" {
				s.client.SetModel(msg.Model)
				s.sendMessage(conn, WSMessage{
					Type:    "system",
					Content: fmt.Sprintf("Model changed to: %s", msg.Model),
				})
			}

		case "clear":
			history = history[:1] // Keep system message
			s.sendMessage(conn, WSMessage{
				Type:    "system",
				Content: "Conversation cleared",
			})
		}
	}
}

func (s *Server) handleChat(conn *websocket.Conn, userMessage string, history *[]client.Message) {
	ctx := context.Background()

	// Add user message
	*history = append(*history, client.Message{
		Role:    "user",
		Content: userMessage,
	})

	tools := s.registry.ToClientTools()

	// Process with potential tool calls
	for {
		// Call API with streaming
		stream, err := s.client.ChatCompletionStream(ctx, *history, tools)
		if err != nil {
			s.sendMessage(conn, WSMessage{Type: "error", Error: err.Error()})
			return
		}

		// Stream the response
		msg, finishReason, err := s.streamResponse(conn, stream)
		stream.Close()

		if err != nil {
			s.sendMessage(conn, WSMessage{Type: "error", Error: err.Error()})
			return
		}

		// Add assistant message to history
		*history = append(*history, *msg)

		// Check for tool calls
		if finishReason == "tool_calls" && len(msg.ToolCalls) > 0 {
			for _, tc := range msg.ToolCalls {
				// Notify tool call
				s.sendMessage(conn, WSMessage{
					Type: "tool_call",
					Tool: tc.Function.Name,
					Args: tc.Function.Arguments,
				})

				// Execute tool
				result, _ := s.executor.ExecuteToolCall(ctx, tc)

				// Send tool result with args for file tracking
				s.sendMessage(conn, WSMessage{
					Type:   "tool_result",
					Tool:   tc.Function.Name,
					Args:   tc.Function.Arguments,
					Result: result.Content,
					Error:  boolToError(result.IsError),
				})

				// Add to history
				*history = append(*history, client.Message{
					Role:       "tool",
					Content:    result.Content,
					ToolCallID: tc.ID,
				})
			}
			continue
		}

		// No more tool calls
		break
	}

	// Signal end of response
	s.sendMessage(conn, WSMessage{Type: "done"})
}

func (s *Server) streamResponse(conn *websocket.Conn, stream *client.StreamReader) (*client.Message, string, error) {
	var content string
	var toolCalls []client.ToolCall
	var finishReason string
	toolCallsMap := make(map[int]*client.ToolCall)

	for {
		chunk, err := stream.Read()
		if err == client.ErrStreamDone {
			break
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, "", err
		}

		if len(chunk.Choices) == 0 {
			continue
		}

		choice := chunk.Choices[0]
		if choice.FinishReason != "" {
			finishReason = choice.FinishReason
		}

		if choice.Delta != nil {
			if choice.Delta.Content != "" {
				content += choice.Delta.Content
				// Stream token to client
				s.sendMessage(conn, WSMessage{
					Type:    "token",
					Content: choice.Delta.Content,
				})
			}

			// Accumulate tool calls
			for _, tc := range choice.Delta.ToolCalls {
				existing, ok := toolCallsMap[tc.Index]
				if !ok {
					toolCallsMap[tc.Index] = &client.ToolCall{
						ID:   tc.ID,
						Type: tc.Type,
						Function: client.FunctionCall{
							Name:      tc.Function.Name,
							Arguments: tc.Function.Arguments,
						},
					}
				} else {
					if tc.ID != "" {
						existing.ID = tc.ID
					}
					if tc.Type != "" {
						existing.Type = tc.Type
					}
					if tc.Function.Name != "" {
						existing.Function.Name = tc.Function.Name
					}
					existing.Function.Arguments += tc.Function.Arguments
				}
			}
		}
	}

	// Convert tool calls map to slice
	for i := 0; i < len(toolCallsMap); i++ {
		if tc, ok := toolCallsMap[i]; ok {
			toolCalls = append(toolCalls, *tc)
		}
	}

	msg := &client.Message{
		Role:      "assistant",
		Content:   content,
		ToolCalls: toolCalls,
	}

	return msg, finishReason, nil
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	models := []string{
		"llama-3.3-70b-versatile",
		"llama-3.1-8b-instant",
		"llama-3.2-90b-vision-preview",
		"mixtral-8x7b-32768",
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"models":  models,
		"current": s.client.Model(),
	})
}

func (s *Server) sendMessage(conn *websocket.Conn, msg WSMessage) {
	data, _ := json.Marshal(msg)
	conn.WriteMessage(websocket.TextMessage, data)
}

func (s *Server) getSystemPrompt() string {
	return `You are groq-go, a web-based AI assistant for software engineering tasks.

You have access to tools for reading, writing, and editing files, searching the codebase, and running shell commands.

## Available Tools
- Read: Read file contents
- Write: Create or overwrite files
- Edit: Replace text in files
- Glob: Find files by pattern
- Grep: Search file contents
- Bash: Execute shell commands
- WebFetch: Fetch web content
- Browser: Take screenshots, get JS-rendered content

Be helpful, concise, and use tools when needed.`
}

func boolToError(isError bool) string {
	if isError {
		return "true"
	}
	return ""
}

// addSecurityHeaders wraps a handler to add security headers
func addSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Allow inline scripts and styles for the app
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; connect-src 'self' ws: wss:;")
		next.ServeHTTP(w, r)
	})
}
