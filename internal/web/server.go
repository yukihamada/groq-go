package web

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"groq-go/internal/auth"
	"groq-go/internal/client"
	"groq-go/internal/knowledge"
	"groq-go/internal/plugin"
	"groq-go/internal/project"
	"groq-go/internal/storage"
	"groq-go/internal/tool"
)

// Time helpers for easier testing
var (
	timeNow      = time.Now
	timeDuration = func(hours int) time.Duration { return time.Duration(hours) }
	timeHour     = time.Hour
)

// Random helper
func randInt(max int) int {
	n, _ := rand.Int(rand.Reader, big.NewInt(int64(max)))
	return int(n.Int64())
}

//go:embed static/*
var staticFiles embed.FS

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins for development
	},
}

// Server represents the web server
type Server struct {
	client    *client.Client
	registry  *tool.Registry
	executor  *tool.Executor
	storage   storage.Storage
	auth      *auth.Manager
	projects  *project.Manager
	knowledge *knowledge.KnowledgeBase
	plugins   *plugin.Manager
	addr      string
	uploadDir string
}

// NewServer creates a new web server
func NewServer(c *client.Client, registry *tool.Registry, kb *knowledge.KnowledgeBase, pm *plugin.Manager, addr string) *Server {
	// Initialize storage
	store, err := storage.NewFileStorage(storage.DefaultStorageDir())
	if err != nil {
		log.Printf("Warning: failed to initialize storage: %v", err)
	}

	// Initialize auth manager
	authManager, err := auth.NewManager()
	if err != nil {
		log.Printf("Warning: failed to initialize auth: %v", err)
	}

	// Initialize project manager
	projectManager, err := project.NewManager()
	if err != nil {
		log.Printf("Warning: failed to initialize project manager: %v", err)
	}

	// Initialize upload directory
	home, _ := os.UserHomeDir()
	uploadDir := filepath.Join(home, ".config", "groq-go", "uploads")
	os.MkdirAll(uploadDir, 0755)

	return &Server{
		client:    c,
		registry:  registry,
		executor:  tool.NewExecutor(registry),
		storage:   store,
		auth:      authManager,
		projects:  projectManager,
		knowledge: kb,
		plugins:   pm,
		addr:      addr,
		uploadDir: uploadDir,
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
	mux.HandleFunc("/api/upload", s.handleUpload)
	mux.HandleFunc("/api/sessions", s.handleSessions)
	mux.HandleFunc("/api/sessions/", s.handleSession)
	mux.HandleFunc("/api/auth/login", s.handleLogin)
	mux.HandleFunc("/api/auth/logout", s.handleLogout)
	mux.HandleFunc("/api/auth/status", s.handleAuthStatus)
	mux.HandleFunc("/api/auth/register", s.handleRegister)
	mux.HandleFunc("/api/projects", s.handleProjects)
	mux.HandleFunc("/api/projects/", s.handleProject)
	mux.HandleFunc("/api/share", s.handleShare)
	mux.HandleFunc("/share/", s.handleSharedView)
	mux.HandleFunc("/api/knowledge", s.handleKnowledge)
	mux.HandleFunc("/api/knowledge/", s.handleKnowledgeDocument)
	mux.HandleFunc("/api/plugins", s.handlePlugins)
	mux.HandleFunc("/api/plugins/", s.handlePlugin)

	log.Printf("Starting web server at http://localhost%s", s.addr)
	return http.ListenAndServe(s.addr, mux)
}

// WSMessage represents WebSocket message types
type WSMessage struct {
	Type     string   `json:"type"`
	Content  string   `json:"content,omitempty"`
	Tool     string   `json:"tool,omitempty"`
	Args     string   `json:"args,omitempty"`
	Result   string   `json:"result,omitempty"`
	Error    string   `json:"error,omitempty"`
	Model    string   `json:"model,omitempty"`
	DiffData string   `json:"diff_data,omitempty"` // For edit tool diffs
	Images   []string `json:"images,omitempty"`    // Base64 image data for vision
	ShareID  string   `json:"share_id,omitempty"`  // For sharing conversations
}

// Store for tracking tool call args
type toolCallInfo struct {
	name string
	args string
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[ERROR] WebSocket upgrade error: %v", err)
		return
	}
	defer conn.Close()

	clientIP := r.RemoteAddr
	log.Printf("[INFO] New WebSocket connection from %s", clientIP)

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
			log.Printf("[CHAT] User (%s): %s", clientIP, truncateLog(msg.Content, 100))
			if len(msg.Images) > 0 {
				log.Printf("[CHAT] With %d image(s)", len(msg.Images))
			}
			mu.Lock()
			s.handleChat(conn, msg.Content, msg.Images, &history, clientIP)
			mu.Unlock()

		case "model":
			if msg.Model != "" {
				log.Printf("[INFO] Model changed to %s by %s", msg.Model, clientIP)
				s.client.SetModel(msg.Model)
				s.sendMessage(conn, WSMessage{
					Type:    "system",
					Content: fmt.Sprintf("Model changed to: %s", msg.Model),
				})
			}

		case "clear":
			log.Printf("[INFO] Conversation cleared by %s", clientIP)
			history = history[:1] // Keep system message
			s.sendMessage(conn, WSMessage{
				Type:    "system",
				Content: "Conversation cleared",
			})
		}
	}
	log.Printf("[INFO] WebSocket connection closed: %s", clientIP)
}

func truncateLog(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func (s *Server) handleChat(conn *websocket.Conn, userMessage string, images []string, history *[]client.Message, clientIP string) {
	ctx := context.Background()

	// Add user message (with images if present)
	var msg client.Message
	if len(images) > 0 {
		// Create multimodal message for vision models
		msg = client.NewVisionMessage("user", userMessage, images...)
	} else {
		msg = client.Message{Role: "user", Content: userMessage}
	}
	*history = append(*history, msg)

	tools := s.registry.ToClientTools()

	// Process with potential tool calls
	for {
		// Call API with streaming
		stream, err := s.client.ChatCompletionStream(ctx, *history, tools)
		if err != nil {
			log.Printf("[ERROR] API error for %s: %v", clientIP, err)
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
				log.Printf("[TOOL] %s calling %s", clientIP, tc.Function.Name)

				// Notify tool call
				s.sendMessage(conn, WSMessage{
					Type: "tool_call",
					Tool: tc.Function.Name,
					Args: tc.Function.Arguments,
				})

				// Execute tool
				result, _ := s.executor.ExecuteToolCall(ctx, tc)

				if result.IsError {
					log.Printf("[TOOL] %s error: %s", tc.Function.Name, truncateLog(result.Content, 100))
				} else {
					log.Printf("[TOOL] %s completed", tc.Function.Name)
				}

				// Extract diff data if present
				resultContent := result.Content
				diffData := ""
				if parts := strings.SplitN(result.Content, "\n---DIFF_DATA---\n", 2); len(parts) == 2 {
					resultContent = parts[0]
					diffData = parts[1]
				}

				// Send tool result with args for file tracking
				s.sendMessage(conn, WSMessage{
					Type:     "tool_result",
					Tool:     tc.Function.Name,
					Args:     tc.Function.Arguments,
					Result:   resultContent,
					Error:    boolToError(result.IsError),
					DiffData: diffData,
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

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse multipart form (max 10MB)
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		http.Error(w, "Failed to parse form", http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "Failed to get file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Read file content
	content, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, "Failed to read file", http.StatusInternalServerError)
		return
	}

	// Save file to upload directory
	filePath := filepath.Join(s.uploadDir, header.Filename)
	if err := os.WriteFile(filePath, content, 0644); err != nil {
		http.Error(w, "Failed to save file", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"path":    filePath,
		"name":    header.Filename,
		"size":    header.Size,
		"content": string(content),
	})
}

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	if s.storage == nil {
		http.Error(w, "Storage not available", http.StatusServiceUnavailable)
		return
	}

	ctx := r.Context()

	switch r.Method {
	case http.MethodGet:
		sessions, err := s.storage.ListSessions(ctx)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(sessions)

	case http.MethodPost:
		var session storage.Session
		if err := json.NewDecoder(r.Body).Decode(&session); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}
		if err := s.storage.SaveSession(ctx, &session); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	if s.storage == nil {
		http.Error(w, "Storage not available", http.StatusServiceUnavailable)
		return
	}

	// Extract session ID from path
	id := filepath.Base(r.URL.Path)
	if id == "" || id == "sessions" {
		http.Error(w, "Session ID required", http.StatusBadRequest)
		return
	}

	ctx := r.Context()

	switch r.Method {
	case http.MethodGet:
		session, err := s.storage.LoadSession(ctx, id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if session == nil {
			http.Error(w, "Session not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(session)

	case http.MethodDelete:
		if err := s.storage.DeleteSession(ctx, id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) sendMessage(conn *websocket.Conn, msg WSMessage) {
	data, _ := json.Marshal(msg)
	conn.WriteMessage(websocket.TextMessage, data)
}

// Auth handlers
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if s.auth == nil {
		// Auth not configured, allow access
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"token":   "no-auth-required",
		})
		return
	}

	token, err := s.auth.Authenticate(req.Username, req.Password)
	if err != nil {
		http.Error(w, "Invalid credentials", http.StatusUnauthorized)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"success":  true,
		"token":    token,
		"username": req.Username,
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	token := r.Header.Get("Authorization")
	if token != "" && s.auth != nil {
		// Remove "Bearer " prefix if present
		if len(token) > 7 && token[:7] == "Bearer " {
			token = token[7:]
		}
		s.auth.InvalidateToken(token)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "logged_out"})
}

func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Check if auth is required
	authRequired := s.auth != nil && s.auth.HasUsers()

	// Check if user is authenticated
	authenticated := false
	var username string

	token := r.Header.Get("Authorization")
	if token != "" && s.auth != nil {
		if len(token) > 7 && token[:7] == "Bearer " {
			token = token[7:]
		}
		if user, err := s.auth.ValidateToken(token); err == nil {
			authenticated = true
			username = user.Username
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"auth_required":  authRequired,
		"authenticated":  authenticated,
		"username":       username,
	})
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.auth == nil {
		http.Error(w, "Auth not available", http.StatusServiceUnavailable)
		return
	}

	// Only allow registration if no users exist (first user setup)
	if s.auth.HasUsers() {
		http.Error(w, "Registration disabled", http.StatusForbidden)
		return
	}

	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Username == "" || req.Password == "" {
		http.Error(w, "Username and password required", http.StatusBadRequest)
		return
	}

	if err := s.auth.CreateUser(req.Username, req.Password); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "created"})
}

// Project handlers
func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	if s.projects == nil {
		http.Error(w, "Projects not available", http.StatusServiceUnavailable)
		return
	}

	switch r.Method {
	case http.MethodGet:
		projects := s.projects.List()
		current := s.projects.Current()
		var currentID string
		if current != nil {
			currentID = current.ID
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"projects": projects,
			"current":  currentID,
		})

	case http.MethodPost:
		var req struct {
			Name        string `json:"name"`
			RootPath    string `json:"root_path"`
			Description string `json:"description"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}
		if req.Name == "" || req.RootPath == "" {
			http.Error(w, "Name and root_path required", http.StatusBadRequest)
			return
		}
		proj, err := s.projects.Create(req.Name, req.RootPath, req.Description)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(proj)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleProject(w http.ResponseWriter, r *http.Request) {
	if s.projects == nil {
		http.Error(w, "Projects not available", http.StatusServiceUnavailable)
		return
	}

	// Extract project ID from path
	id := filepath.Base(r.URL.Path)
	if id == "" || id == "projects" {
		http.Error(w, "Project ID required", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		proj, err := s.projects.Get(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(proj)

	case http.MethodPut:
		var req struct {
			Name        string `json:"name"`
			RootPath    string `json:"root_path"`
			Description string `json:"description"`
			SetCurrent  bool   `json:"set_current"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}
		if req.SetCurrent {
			if err := s.projects.SetCurrent(id); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		if req.Name != "" || req.RootPath != "" || req.Description != "" {
			if err := s.projects.Update(id, req.Name, req.RootPath, req.Description); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "updated"})

	case http.MethodDelete:
		if err := s.projects.Delete(id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// Share handlers
func (s *Server) handleShare(w http.ResponseWriter, r *http.Request) {
	if s.storage == nil {
		http.Error(w, "Storage not available", http.StatusServiceUnavailable)
		return
	}

	ctx := r.Context()

	switch r.Method {
	case http.MethodPost:
		var req struct {
			SessionID string           `json:"session_id"`
			Title     string           `json:"title"`
			Messages  []client.Message `json:"messages"`
			ExpiresIn int              `json:"expires_in"` // hours, 0 = never
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		// Generate share ID
		shareID := generateShareID()

		share := &storage.SharedConversation{
			ShareID:   shareID,
			SessionID: req.SessionID,
			Title:     req.Title,
			Messages:  req.Messages,
			CreatedAt: timeNow(),
			ViewCount: 0,
		}

		if req.ExpiresIn > 0 {
			share.ExpiresAt = timeNow().Add(timeDuration(req.ExpiresIn) * timeHour)
		}

		if err := s.storage.SaveShare(ctx, share); err != nil {
			log.Printf("[ERROR] Failed to save share: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		log.Printf("[INFO] Created share link: %s", shareID)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"share_id":  shareID,
			"share_url": "/share/" + shareID,
		})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleSharedView(w http.ResponseWriter, r *http.Request) {
	if s.storage == nil {
		http.Error(w, "Storage not available", http.StatusServiceUnavailable)
		return
	}

	// Extract share ID from path
	shareID := strings.TrimPrefix(r.URL.Path, "/share/")
	if shareID == "" {
		http.Error(w, "Share ID required", http.StatusBadRequest)
		return
	}

	ctx := r.Context()

	share, err := s.storage.LoadShare(ctx, shareID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if share == nil {
		http.Error(w, "Share not found", http.StatusNotFound)
		return
	}

	// Check expiration
	if !share.ExpiresAt.IsZero() && timeNow().After(share.ExpiresAt) {
		http.Error(w, "This share link has expired", http.StatusGone)
		return
	}

	// Increment view count
	s.storage.IncrementShareViewCount(ctx, shareID)

	// Check Accept header to determine response type
	accept := r.Header.Get("Accept")
	if strings.Contains(accept, "application/json") {
		// Return JSON for API requests
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(share)
		return
	}

	// Return HTML page for browser requests
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, sharedViewHTML, share.Title, share.Title, formatMessagesHTML(share.Messages), share.ViewCount)
}

func generateShareID() string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, 12)
	for i := range b {
		b[i] = charset[randInt(len(charset))]
	}
	return string(b)
}

func formatMessagesHTML(messages []client.Message) string {
	var sb strings.Builder
	for _, msg := range messages {
		if msg.Role == "system" {
			continue
		}
		roleClass := msg.Role
		content := ""
		switch c := msg.Content.(type) {
		case string:
			content = c
		case []any:
			for _, part := range c {
				if p, ok := part.(map[string]any); ok {
					if text, ok := p["text"].(string); ok {
						content += text
					}
				}
			}
		}
		sb.WriteString(fmt.Sprintf(`<div class="message %s"><strong>%s:</strong> %s</div>`, roleClass, msg.Role, content))
	}
	return sb.String()
}

const sharedViewHTML = `<!DOCTYPE html>
<html lang="ja">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>%s - groq-go</title>
    <link href="https://cdn.jsdelivr.net/npm/prismjs@1/themes/prism-tomorrow.css" rel="stylesheet">
    <script src="https://cdn.jsdelivr.net/npm/marked/marked.min.js"></script>
    <script src="https://cdn.jsdelivr.net/npm/prismjs@1/prism.min.js"></script>
    <style>
        * { box-sizing: border-box; margin: 0; padding: 0; }
        body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; background: #1a1a2e; color: #eee; padding: 20px; }
        .container { max-width: 900px; margin: 0 auto; }
        h1 { margin-bottom: 20px; color: #fff; }
        .message { padding: 15px; margin: 10px 0; border-radius: 10px; }
        .message.user { background: #16213e; }
        .message.assistant { background: #0f3460; }
        .message strong { color: #e94560; }
        .view-count { color: #888; font-size: 0.9em; margin-top: 20px; }
        pre { background: #2d2d2d; padding: 10px; border-radius: 5px; overflow-x: auto; }
        code { font-family: 'Fira Code', monospace; }
    </style>
</head>
<body>
    <div class="container">
        <h1>%s</h1>
        <div id="messages">%s</div>
        <p class="view-count">Views: %d</p>
    </div>
    <script>
        document.querySelectorAll('.message').forEach(el => {
            const text = el.innerHTML;
            el.innerHTML = marked.parse(text);
        });
        Prism.highlightAll();
    </script>
</body>
</html>
`

// Knowledge handlers
func (s *Server) handleKnowledge(w http.ResponseWriter, r *http.Request) {
	if s.knowledge == nil {
		http.Error(w, "Knowledge base not available", http.StatusServiceUnavailable)
		return
	}

	ctx := r.Context()

	switch r.Method {
	case http.MethodGet:
		docs := s.knowledge.ListDocuments(ctx)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"documents": docs,
			"count":     len(docs),
		})

	case http.MethodPost:
		var req struct {
			Name    string `json:"name"`
			Content string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}
		if req.Name == "" || req.Content == "" {
			http.Error(w, "Name and content are required", http.StatusBadRequest)
			return
		}

		doc, err := s.knowledge.AddDocument(ctx, req.Name, req.Content)
		if err != nil {
			log.Printf("[ERROR] Failed to add document: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		log.Printf("[INFO] Added document to knowledge base: %s", doc.Name)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(doc)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleKnowledgeDocument(w http.ResponseWriter, r *http.Request) {
	if s.knowledge == nil {
		http.Error(w, "Knowledge base not available", http.StatusServiceUnavailable)
		return
	}

	// Extract document ID from path
	docID := strings.TrimPrefix(r.URL.Path, "/api/knowledge/")
	if docID == "" {
		http.Error(w, "Document ID required", http.StatusBadRequest)
		return
	}

	ctx := r.Context()

	switch r.Method {
	case http.MethodGet:
		doc, err := s.knowledge.GetDocument(ctx, docID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(doc)

	case http.MethodDelete:
		if err := s.knowledge.DeleteDocument(ctx, docID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		log.Printf("[INFO] Deleted document from knowledge base: %s", docID)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// Plugin handlers
func (s *Server) handlePlugins(w http.ResponseWriter, r *http.Request) {
	if s.plugins == nil {
		http.Error(w, "Plugin manager not available", http.StatusServiceUnavailable)
		return
	}

	switch r.Method {
	case http.MethodGet:
		plugins := s.plugins.ListPlugins()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"plugins": plugins,
			"count":   len(plugins),
		})

	case http.MethodPost:
		var req plugin.Plugin
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}
		if req.Name == "" {
			http.Error(w, "Plugin name is required", http.StatusBadRequest)
			return
		}

		if err := s.plugins.AddPlugin(&req); err != nil {
			log.Printf("[ERROR] Failed to add plugin: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		log.Printf("[INFO] Added plugin: %s", req.Name)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "added"})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handlePlugin(w http.ResponseWriter, r *http.Request) {
	if s.plugins == nil {
		http.Error(w, "Plugin manager not available", http.StatusServiceUnavailable)
		return
	}

	// Extract plugin name from path
	name := strings.TrimPrefix(r.URL.Path, "/api/plugins/")
	if name == "" {
		http.Error(w, "Plugin name required", http.StatusBadRequest)
		return
	}

	// Handle action suffix (e.g., /api/plugins/myPlugin/enable)
	parts := strings.Split(name, "/")
	name = parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}

	switch r.Method {
	case http.MethodGet:
		p, ok := s.plugins.GetPlugin(name)
		if !ok {
			http.Error(w, "Plugin not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(p)

	case http.MethodPut:
		var err error
		switch action {
		case "enable":
			err = s.plugins.EnablePlugin(name)
			if err == nil {
				log.Printf("[INFO] Enabled plugin: %s", name)
			}
		case "disable":
			err = s.plugins.DisablePlugin(name)
			if err == nil {
				log.Printf("[INFO] Disabled plugin: %s", name)
			}
		default:
			http.Error(w, "Unknown action", http.StatusBadRequest)
			return
		}

		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})

	case http.MethodDelete:
		if err := s.plugins.RemovePlugin(name); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		log.Printf("[INFO] Removed plugin: %s", name)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) getSystemPrompt() string {
	return `You are groq-go, a web-based AI assistant for software engineering tasks.

You have access to tools for reading, writing, and editing files, searching the codebase, running shell commands, managing git repositories, and generating images.

## Available Tools
- Read: Read file contents
- Write: Create or overwrite files (ALWAYS use this for creating files, NOT bash echo/cat)
- Edit: Replace text in files
- Glob: Find files by pattern
- Grep: Search file contents
- Bash: Execute shell commands (for running programs, NOT for creating files)
- WebFetch: Fetch web content
- Browser: Take screenshots, get JS-rendered content
- Git: Execute git commands (status, diff, log, add, commit, push, pull, branch, checkout, stash)
- ImageGen: Generate images from text prompts (requires STABILITY_API_KEY or OPENAI_API_KEY)
- CodeExec: Execute code in a sandbox (JavaScript, Python, Go, shell)
- KnowledgeSearch: Search the knowledge base for relevant information
- KnowledgeList: List documents in the knowledge base

## Important Rules
1. ALWAYS use the Write tool to create files. NEVER use bash echo, cat, or heredoc to create files.
2. When creating web apps, put ALL HTML, CSS, and JavaScript in a SINGLE .html file using <style> and <script> tags. Do NOT create separate .css or .js files.
3. Created HTML files will be shown in the preview panel automatically.
4. Use the Git tool for all git operations instead of running git via Bash.
5. Be helpful, concise, and use tools when needed.`
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
		// Allow inline scripts and styles for the app, plus CDN for libraries
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; "+
				"script-src 'self' 'unsafe-inline' https://cdn.jsdelivr.net; "+
				"style-src 'self' 'unsafe-inline' https://cdn.jsdelivr.net; "+
				"connect-src 'self' ws: wss:; "+
				"img-src 'self' data: blob:; "+
				"font-src 'self' https://cdn.jsdelivr.net;")
		next.ServeHTTP(w, r)
	})
}
