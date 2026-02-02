package web

import (
	"bytes"
	"context"
	"crypto/rand"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
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
	"groq-go/internal/credits"
	"groq-go/internal/knowledge"
	"groq-go/internal/logging"
	"groq-go/internal/plugin"
	"groq-go/internal/project"
	"groq-go/internal/storage"
	"groq-go/internal/tool"
	"groq-go/internal/version"
)

// Logger for web package
var log = logging.WithComponent("web")

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

// Allowed origins for WebSocket connections
var allowedOrigins = map[string]bool{
	"localhost":            true,
	"127.0.0.1":            true,
	"groq-go-yuki.fly.dev": true,
	"chatweb.ai":           true,
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true // Allow non-browser clients
		}
		// Parse origin and check against allowlist
		for allowed := range allowedOrigins {
			if strings.Contains(origin, allowed) {
				return true
			}
		}
		log.Warn("Blocked WebSocket connection", "origin", origin)
		return false
	},
}

// Rate limiter for API endpoints
type rateLimiter struct {
	mu       sync.Mutex
	clients  map[string]*clientRate
	maxReqs  int
	window   time.Duration
}

type clientRate struct {
	count    int
	resetAt  time.Time
}

var apiLimiter = &rateLimiter{
	clients: make(map[string]*clientRate),
	maxReqs: 60,              // 60 requests
	window:  time.Minute,     // per minute
}

func (rl *rateLimiter) allow(clientIP string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	client, exists := rl.clients[clientIP]
	if !exists || now.After(client.resetAt) {
		rl.clients[clientIP] = &clientRate{count: 1, resetAt: now.Add(rl.window)}
		return true
	}
	if client.count >= rl.maxReqs {
		return false
	}
	client.count++
	return true
}

// Server represents the web server
type Server struct {
	client       *client.Client
	registry     *tool.Registry
	executor     *tool.Executor
	storage      storage.Storage
	auth         *auth.Manager
	projects     *project.Manager
	knowledge    *knowledge.KnowledgeBase
	plugins      *plugin.Manager
	versions     *version.Manager
	versionProxy *version.Proxy
	credits      *credits.Manager
	addr         string
	uploadDir    string
}

// NewServer creates a new web server
func NewServer(c *client.Client, registry *tool.Registry, kb *knowledge.KnowledgeBase, pm *plugin.Manager, vm *version.Manager, addr string) *Server {
	// Initialize storage
	store, err := storage.NewFileStorage(storage.DefaultStorageDir())
	if err != nil {
		log.Warn("Failed to initialize storage", "error", err)
	}

	// Initialize auth manager
	authManager, err := auth.NewManager()
	if err != nil {
		log.Warn("Failed to initialize auth", "error", err)
	}

	// Initialize project manager
	projectManager, err := project.NewManager()
	if err != nil {
		log.Warn("Failed to initialize project manager", "error", err)
	}

	// Initialize upload directory
	home, _ := os.UserHomeDir()
	uploadDir := filepath.Join(home, ".config", "groq-go", "uploads")
	os.MkdirAll(uploadDir, 0755)

	// Initialize version proxy if version manager is available
	var versionProxy *version.Proxy
	if vm != nil {
		// Get main domain from environment or default
		mainDomain := os.Getenv("MAIN_DOMAIN")
		if mainDomain == "" {
			mainDomain = "chatweb.ai"
		}
		versionProxy = version.NewProxy(vm, mainDomain)
	}

	// Initialize credits manager
	creditsManager, err := credits.NewManager()
	if err != nil {
		log.Warn("Failed to initialize credits manager", "error", err)
	}

	return &Server{
		client:       c,
		registry:     registry,
		executor:     tool.NewExecutor(registry),
		storage:      store,
		auth:         authManager,
		projects:     projectManager,
		knowledge:    kb,
		plugins:      pm,
		versions:     vm,
		versionProxy: versionProxy,
		credits:      creditsManager,
		addr:         addr,
		uploadDir:    uploadDir,
	}
}

// rateLimitMiddleware wraps handlers with rate limiting
func rateLimitMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		clientIP := r.RemoteAddr
		if fwdFor := r.Header.Get("X-Forwarded-For"); fwdFor != "" {
			clientIP = strings.Split(fwdFor, ",")[0]
		}
		if !apiLimiter.allow(clientIP) {
			log.Warn("Rate limit exceeded", "client_ip", clientIP)
			http.Error(w, "Too many requests", http.StatusTooManyRequests)
			return
		}
		next(w, r)
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

	// WebSocket endpoint (no rate limit - managed separately)
	mux.HandleFunc("/ws", s.handleWebSocket)

	// API endpoints with rate limiting
	mux.HandleFunc("/api/models", rateLimitMiddleware(s.handleModels))
	mux.HandleFunc("/api/upload", rateLimitMiddleware(s.handleUpload))
	mux.HandleFunc("/api/sessions", rateLimitMiddleware(s.handleSessions))
	mux.HandleFunc("/api/sessions/", rateLimitMiddleware(s.handleSession))
	mux.HandleFunc("/api/auth/login", rateLimitMiddleware(s.handleLogin))
	mux.HandleFunc("/api/auth/logout", rateLimitMiddleware(s.handleLogout))
	mux.HandleFunc("/api/auth/status", rateLimitMiddleware(s.handleAuthStatus))
	mux.HandleFunc("/api/auth/register", rateLimitMiddleware(s.handleRegister))
	mux.HandleFunc("/api/projects", rateLimitMiddleware(s.handleProjects))
	mux.HandleFunc("/api/projects/", rateLimitMiddleware(s.handleProject))
	mux.HandleFunc("/api/share", rateLimitMiddleware(s.handleShare))
	mux.HandleFunc("/share/", s.handleSharedView) // Public endpoint, no auth
	mux.HandleFunc("/api/knowledge", rateLimitMiddleware(s.handleKnowledge))
	mux.HandleFunc("/api/knowledge/", rateLimitMiddleware(s.handleKnowledgeDocument))
	mux.HandleFunc("/api/plugins", rateLimitMiddleware(s.handlePlugins))
	mux.HandleFunc("/api/plugins/", rateLimitMiddleware(s.handlePlugin))
	mux.HandleFunc("/api/tts", rateLimitMiddleware(s.handleTTS))
	mux.HandleFunc("/api/tts/elevenlabs", rateLimitMiddleware(s.handleElevenLabsTTS))

	// Version management endpoints
	mux.HandleFunc("/api/versions", rateLimitMiddleware(s.handleVersions))
	mux.HandleFunc("/api/versions/", rateLimitMiddleware(s.handleVersion))

	// Credit management endpoints
	mux.HandleFunc("/api/credits", rateLimitMiddleware(s.handleCredits))
	mux.HandleFunc("/api/credits/", rateLimitMiddleware(s.handleCreditAction))

	log.Info("Starting web server", "addr", s.addr)

	// Wrap with version proxy if available
	var handler http.Handler = mux
	if s.versionProxy != nil {
		handler = s.versionProxy.ProxyHandler(mux)
		log.Info("Version proxy enabled", "domain", os.Getenv("MAIN_DOMAIN"))
	}

	return http.ListenAndServe(s.addr, handler)
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
	Mode     string   `json:"mode,omitempty"`      // "tools" or "improve"
}

// Store for tracking tool call args
type toolCallInfo struct {
	name string
	args string
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Error("WebSocket upgrade failed", "error", err)
		return
	}
	defer conn.Close()

	clientIP := r.RemoteAddr
	if fwdFor := r.Header.Get("X-Forwarded-For"); fwdFor != "" {
		clientIP = strings.Split(fwdFor, ",")[0]
	}
	log.Info("New WebSocket connection", "client_ip", clientIP)

	// Create or get user based on IP (can be enhanced with proper auth later)
	userID := "user_" + strings.ReplaceAll(strings.ReplaceAll(clientIP, ".", "_"), ":", "_")
	var userCredits *credits.UserCredits
	if s.credits != nil {
		userCredits = s.credits.GetOrCreateUser(userID, "")
		log.Info("User credits", "user_id", userID, "balance", userCredits.Balance)
	}

	// Send welcome message with credit info
	welcomeMsg := fmt.Sprintf("Connected to groq-go. Model: %s", s.client.Model())
	if userCredits != nil {
		welcomeMsg += fmt.Sprintf(" | Credits: %d", userCredits.Balance)
	}
	s.sendMessage(conn, WSMessage{
		Type:    "system",
		Content: welcomeMsg,
	})

	// Message history for this session
	var history []client.Message
	currentMode := "tools" // Default mode: tools

	history = append(history, client.Message{
		Role:    "system",
		Content: s.getSystemPrompt(currentMode),
	})

	var mu sync.Mutex

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Error("WebSocket read error", "error", err)
			}
			break
		}

		var msg WSMessage
		if err := json.Unmarshal(message, &msg); err != nil {
			s.sendMessage(conn, WSMessage{Type: "error", Error: "Invalid message format"})
			continue
		}

		switch msg.Type {
		case "mode":
			// Handle mode change
			if msg.Mode == "tools" || msg.Mode == "improve" {
				currentMode = msg.Mode
				// Update system prompt in history
				history[0] = client.Message{
					Role:    "system",
					Content: s.getSystemPrompt(currentMode),
				}
				log.Info("Mode changed", "mode", currentMode, "client_ip", clientIP)
			}

		case "chat":
			log.Debug("User message", "client_ip", clientIP, "content", truncateLog(msg.Content, 100))
			if len(msg.Images) > 0 {
				log.Debug("Message includes images", "count", len(msg.Images))
			}
			// Update mode if provided with chat message
			if msg.Mode != "" && (msg.Mode == "tools" || msg.Mode == "improve") {
				currentMode = msg.Mode
				history[0] = client.Message{
					Role:    "system",
					Content: s.getSystemPrompt(currentMode),
				}
			}
			mu.Lock()
			s.handleChat(conn, msg.Content, msg.Images, &history, clientIP, userID, currentMode)
			mu.Unlock()

		case "model":
			if msg.Model != "" {
				log.Info("Model changed", "model", msg.Model, "client_ip", clientIP)
				s.client.SetModel(msg.Model)
				s.sendMessage(conn, WSMessage{
					Type:    "system",
					Content: fmt.Sprintf("Model changed to: %s", msg.Model),
				})
			}

		case "clear":
			log.Info("Conversation cleared", "client_ip", clientIP)
			history = history[:1] // Keep system message
			s.sendMessage(conn, WSMessage{
				Type:    "system",
				Content: "Conversation cleared",
			})
		}
	}
	log.Info("WebSocket connection closed", "client_ip", clientIP)
}

func truncateLog(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func (s *Server) handleChat(conn *websocket.Conn, userMessage string, images []string, history *[]client.Message, clientIP string, userID string, mode string) {
	ctx := context.Background()

	// Check credits before processing
	model := s.client.Model()
	if s.credits != nil {
		hasCredits, balance, cost := s.credits.CheckCredits(userID, model)
		if !hasCredits {
			s.sendMessage(conn, WSMessage{
				Type:  "error",
				Error: fmt.Sprintf("Insufficient credits: need %d, have %d. Please add more credits.", cost, balance),
			})
			s.sendMessage(conn, WSMessage{Type: "done"})
			return
		}
	}

	// Add user message (with images if present)
	var msg client.Message
	if len(images) > 0 {
		// Create multimodal message for vision models
		msg = client.NewVisionMessage("user", userMessage, images...)
	} else {
		msg = client.Message{Role: "user", Content: userMessage}
	}
	*history = append(*history, msg)

	// Get tools based on mode
	var tools []client.Tool
	if mode == "improve" {
		// Improvement mode: only SelfImprove tool
		tools = s.registry.ToClientToolsFiltered([]string{"SelfImprove"})
	} else {
		// Tools mode: all tools except SelfImprove (unless explicitly needed)
		tools = s.registry.ToClientTools()
	}

	// Process with potential tool calls
	for {
		// Call API with streaming
		stream, err := s.client.ChatCompletionStream(ctx, *history, tools)
		if err != nil {
			log.Error("API error", "client_ip", clientIP, "error", err)
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
				log.Debug("Tool call", "client_ip", clientIP, "tool", tc.Function.Name)

				// Notify tool call
				s.sendMessage(conn, WSMessage{
					Type: "tool_call",
					Tool: tc.Function.Name,
					Args: tc.Function.Arguments,
				})

				// Execute tool
				result, _ := s.executor.ExecuteToolCall(ctx, tc)

				if result.IsError {
					log.Error("Tool execution error", "tool", tc.Function.Name, "error", truncateLog(result.Content, 100))
				} else {
					log.Debug("Tool completed", "tool", tc.Function.Name)
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

	// Deduct credits after successful completion
	if s.credits != nil {
		if err := s.credits.UseCredits(userID, model, 0); err != nil {
			log.Warn("Failed to deduct credits", "user_id", userID, "error", err)
		} else {
			// Send updated balance
			balance := s.credits.GetBalance(userID)
			s.sendMessage(conn, WSMessage{
				Type:    "credits",
				Content: fmt.Sprintf("%d", balance),
			})
		}
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
		// Groq models
		"llama-3.3-70b-versatile",
		"llama-3.1-8b-instant",
		"llama-3.2-90b-vision-preview",
		"mixtral-8x7b-32768",
		// Claude models
		"claude-sonnet-4-20250514",
		"claude-3-5-sonnet-20241022",
		"claude-3-5-haiku-20241022",
		"claude-3-opus-20240229",
		// OpenAI models
		"gpt-4o",
		"gpt-4o-mini",
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

func (s *Server) sendMessage(conn *websocket.Conn, msg WSMessage) error {
	data, err := json.Marshal(msg)
	if err != nil {
		log.Error("Failed to marshal WebSocket message", "error", err)
		return err
	}
	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		log.Error("Failed to write WebSocket message", "error", err)
		return err
	}
	return nil
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
			log.Error("Failed to save share", "error", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		log.Info("Created share link", "share_id", shareID)

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
			log.Error("Failed to add document to knowledge base", "error", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		log.Info("Added document to knowledge base", "name", doc.Name)

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
		log.Info("Deleted document from knowledge base", "doc_id", docID)
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
			log.Error("Failed to add plugin", "error", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		log.Info("Added plugin", "name", req.Name)

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
				log.Info("Enabled plugin", "name", name)
			}
		case "disable":
			err = s.plugins.DisablePlugin(name)
			if err == nil {
				log.Info("Disabled plugin", "name", name)
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
		log.Info("Removed plugin", "name", name)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleTTS handles text-to-speech requests using Kokoro TTS
func (s *Server) handleTTS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Text  string `json:"text"`
		Voice string `json:"voice"`
		Speed float64 `json:"speed"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Text == "" {
		http.Error(w, "Text is required", http.StatusBadRequest)
		return
	}

	// Default values
	if req.Voice == "" {
		req.Voice = "jf_alpha"
	}
	if req.Speed == 0 {
		req.Speed = 1.0
	}

	// Get FAL API key
	falAPIKey := os.Getenv("FAL_API_KEY")
	if falAPIKey == "" {
		// Fallback: return empty response, let client use Web Speech API
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"fallback": true})
		return
	}

	// Call Kokoro TTS API
	ttsReq := map[string]any{
		"prompt": req.Text,
		"voice":  req.Voice,
		"speed":  req.Speed,
	}
	ttsBody, _ := json.Marshal(ttsReq)

	httpReq, err := http.NewRequest("POST", "https://fal.run/fal-ai/kokoro/japanese", bytes.NewReader(ttsBody))
	if err != nil {
		http.Error(w, "Failed to create request", http.StatusInternalServerError)
		return
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Key "+falAPIKey)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		log.Error("TTS request failed", "error", err)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"fallback": true, "error": err.Error()})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Error("TTS API error", "response", string(body))
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"fallback": true, "error": "TTS API error"})
		return
	}

	// Parse response and return audio URL
	var ttsResp struct {
		Audio struct {
			URL string `json:"url"`
		} `json:"audio"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ttsResp); err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"fallback": true})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"audio_url": ttsResp.Audio.URL,
	})
}

// handleElevenLabsTTS handles text-to-speech using ElevenLabs API
func (s *Server) handleElevenLabsTTS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Text    string  `json:"text"`
		VoiceID string  `json:"voice_id"`
		ModelID string  `json:"model_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Text == "" {
		http.Error(w, "Text is required", http.StatusBadRequest)
		return
	}

	// Get ElevenLabs API key from environment
	apiKey := os.Getenv("ELEVENLABS_API_KEY")
	if apiKey == "" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"fallback": true, "error": "ElevenLabs API key not configured"})
		return
	}

	// Default voice ID (can be overridden)
	voiceID := req.VoiceID
	if voiceID == "" {
		voiceID = os.Getenv("ELEVENLABS_VOICE_ID")
		if voiceID == "" {
			voiceID = "21m00Tcm4TlvDq8ikWAM" // Default: Rachel
		}
	}

	// Default model
	modelID := req.ModelID
	if modelID == "" {
		modelID = "eleven_multilingual_v2"
	}

	// Call ElevenLabs API
	ttsReq := map[string]any{
		"text":     req.Text,
		"model_id": modelID,
		"voice_settings": map[string]any{
			"stability":        0.5,
			"similarity_boost": 0.75,
		},
	}
	ttsBody, _ := json.Marshal(ttsReq)

	url := fmt.Sprintf("https://api.elevenlabs.io/v1/text-to-speech/%s", voiceID)
	httpReq, err := http.NewRequest("POST", url, bytes.NewReader(ttsBody))
	if err != nil {
		http.Error(w, "Failed to create request", http.StatusInternalServerError)
		return
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("xi-api-key", apiKey)
	httpReq.Header.Set("Accept", "audio/mpeg")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		log.Error("ElevenLabs TTS request failed", "error", err)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"fallback": true, "error": err.Error()})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Error("ElevenLabs API error", "status", resp.StatusCode, "response", string(body))
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"fallback": true, "error": "ElevenLabs API error"})
		return
	}

	// Stream audio directly to client
	w.Header().Set("Content-Type", "audio/mpeg")
	io.Copy(w, resp.Body)
}

func (s *Server) getSystemPrompt(mode string) string {
	if mode == "improve" {
		return `You are groq-go in IMPROVEMENT MODE. Your primary purpose is to improve your own source code.

## Available Tool
You only have access to the SelfImprove tool in this mode.

## SelfImprove Actions
- "list": List source files (use pattern to filter)
- "read": Read a source file
- "write": Modify a source file
- "status": Show git status
- "diff": Show uncommitted changes
- "verify_build": Test if code compiles (ALWAYS do this before pushing!)
- "commit": Commit changes with a message
- "safe_push": Push only if build succeeds + mark as known good
- "rollback": Rollback to previous commit
- "rollback_safe": Rollback to last known good
- "fly_rollback": Get Fly.io rollback instructions
- "history": Show commit history

## Safe Deployment Protocol
1. Make changes with "write"
2. Check with "diff"
3. Verify with "verify_build"
4. Commit with "commit"
5. Deploy with "safe_push"
6. If broken: "rollback_safe" or "fly_rollback"

## Guidelines
- Be careful with changes - they affect the live system
- Always verify build before pushing
- Keep changes small and focused
- Test thoroughly before deploying`
	}

	// Default: Tools mode
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

// Version management handlers
func (s *Server) handleVersions(w http.ResponseWriter, r *http.Request) {
	if s.versions == nil {
		http.Error(w, "Version management not available", http.StatusServiceUnavailable)
		return
	}

	ctx := r.Context()

	switch r.Method {
	case http.MethodGet:
		versions := s.versions.ListVersions()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"versions": versions,
			"count":    len(versions),
		})

	case http.MethodPost:
		var req struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}
		if req.Name == "" {
			http.Error(w, "Name is required", http.StatusBadRequest)
			return
		}

		v, err := s.versions.CreateVersion(ctx, req.Name, req.Description)
		if err != nil {
			log.Error("Failed to create version", "error", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		log.Info("Created version", "id", v.ID, "name", v.Name)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(v)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// Credit management handlers
func (s *Server) handleCredits(w http.ResponseWriter, r *http.Request) {
	if s.credits == nil {
		http.Error(w, "Credits not available", http.StatusServiceUnavailable)
		return
	}

	// Get user ID from request (IP-based for now)
	clientIP := r.RemoteAddr
	if fwdFor := r.Header.Get("X-Forwarded-For"); fwdFor != "" {
		clientIP = strings.Split(fwdFor, ",")[0]
	}
	userID := "user_" + strings.ReplaceAll(strings.ReplaceAll(clientIP, ".", "_"), ":", "_")

	switch r.Method {
	case http.MethodGet:
		user := s.credits.GetOrCreateUser(userID, "")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"user_id":      user.UserID,
			"balance":      user.Balance,
			"total_used":   user.TotalUsed,
			"total_bought": user.TotalBought,
			"free_credits": user.FreeCredits,
			"costs":        credits.CreditCost,
		})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleCreditAction(w http.ResponseWriter, r *http.Request) {
	if s.credits == nil {
		http.Error(w, "Credits not available", http.StatusServiceUnavailable)
		return
	}

	// Get user ID from request
	clientIP := r.RemoteAddr
	if fwdFor := r.Header.Get("X-Forwarded-For"); fwdFor != "" {
		clientIP = strings.Split(fwdFor, ",")[0]
	}
	userID := "user_" + strings.ReplaceAll(strings.ReplaceAll(clientIP, ".", "_"), ":", "_")

	// Extract action from path: /api/credits/{action}
	action := strings.TrimPrefix(r.URL.Path, "/api/credits/")

	switch action {
	case "history":
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		user := s.credits.GetUserInfo(userID)
		if user == nil {
			http.Error(w, "User not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"transactions": user.Transactions,
		})

	case "add":
		// Admin endpoint to add credits (should be protected in production)
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			UserID string `json:"user_id"`
			Amount int    `json:"amount"`
			Type   string `json:"type"` // "free" or "buy"
			Note   string `json:"note"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}
		targetUserID := req.UserID
		if targetUserID == "" {
			targetUserID = userID
		}
		if req.Type == "" {
			req.Type = "free"
		}
		if err := s.credits.AddCredits(targetUserID, req.Amount, req.Type, req.Note); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		log.Info("Added credits", "user_id", targetUserID, "amount", req.Amount, "type", req.Type)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})

	default:
		http.Error(w, "Unknown action: "+action, http.StatusBadRequest)
	}
}

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	if s.versions == nil {
		http.Error(w, "Version management not available", http.StatusServiceUnavailable)
		return
	}

	// Extract version ID and action from path
	// /api/versions/{id} or /api/versions/{id}/{action}
	path := strings.TrimPrefix(r.URL.Path, "/api/versions/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		http.Error(w, "Version ID required", http.StatusBadRequest)
		return
	}

	id := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}

	ctx := r.Context()

	// Handle actions
	if action != "" && r.Method == http.MethodPost {
		switch action {
		case "build":
			if err := s.versions.BuildVersion(ctx, id); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			log.Info("Built version", "id", id)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"status": "built"})
			return

		case "start":
			if err := s.versions.StartVersion(ctx, id); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			v, _ := s.versions.GetVersion(id)
			log.Info("Started version", "id", id, "port", v.Port)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"status": "started",
				"port":   v.Port,
			})
			return

		case "stop":
			if err := s.versions.StopVersion(ctx, id); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			log.Info("Stopped version", "id", id)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"status": "stopped"})
			return

		case "restart":
			if err := s.versions.RestartVersion(ctx, id); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			v, _ := s.versions.GetVersion(id)
			log.Info("Restarted version", "id", id, "port", v.Port)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"status": "restarted",
				"port":   v.Port,
			})
			return

		default:
			http.Error(w, "Unknown action: "+action, http.StatusBadRequest)
			return
		}
	}

	// Handle logs action (GET)
	if action == "logs" && r.Method == http.MethodGet {
		logs, err := s.versions.GetVersionLogs(id, 100)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"logs": logs})
		return
	}

	switch r.Method {
	case http.MethodGet:
		v, ok := s.versions.GetVersion(id)
		if !ok {
			http.Error(w, "Version not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(v)

	case http.MethodDelete:
		if err := s.versions.DeleteVersion(ctx, id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		log.Info("Deleted version", "id", id)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}
