package auth

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gopkg.in/yaml.v3"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrUserNotFound       = errors.New("user not found")
	ErrUserExists         = errors.New("user already exists")
	ErrInvalidToken       = errors.New("invalid or expired token")
)

// User represents a user account
type User struct {
	Username     string `yaml:"username" json:"username"`
	PasswordHash string `yaml:"password_hash" json:"-"`
	CreatedAt    string `yaml:"created_at" json:"created_at"`
}

// Token represents an authentication token
type Token struct {
	Value     string
	Username  string
	ExpiresAt time.Time
}

// Config represents the auth configuration file
type Config struct {
	Users []User `yaml:"users"`
}

// Manager handles authentication
type Manager struct {
	mu       sync.RWMutex
	users    map[string]*User
	tokens   map[string]*Token
	configPath string
}

// NewManager creates a new auth manager
func NewManager() (*Manager, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}

	configPath := filepath.Join(home, ".config", "groq-go", "users.yaml")

	m := &Manager{
		users:      make(map[string]*User),
		tokens:     make(map[string]*Token),
		configPath: configPath,
	}

	// Load existing users
	if err := m.loadConfig(); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to load auth config: %w", err)
	}

	return m, nil
}

func (m *Manager) loadConfig() error {
	data, err := os.ReadFile(m.configPath)
	if err != nil {
		return err
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("failed to parse config: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, user := range config.Users {
		u := user // Copy
		m.users[user.Username] = &u
	}

	return nil
}

func (m *Manager) saveConfig() error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var users []User
	for _, user := range m.users {
		users = append(users, *user)
	}

	config := Config{Users: users}

	data, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(m.configPath), 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	if err := os.WriteFile(m.configPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	return nil
}

// CreateUser creates a new user
func (m *Manager) CreateUser(username, password string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.users[username]; exists {
		return ErrUserExists
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}

	m.users[username] = &User{
		Username:     username,
		PasswordHash: string(hash),
		CreatedAt:    time.Now().Format(time.RFC3339),
	}

	// Save immediately
	m.mu.Unlock()
	err = m.saveConfig()
	m.mu.Lock()

	return err
}

// Authenticate validates credentials and returns a token
func (m *Manager) Authenticate(username, password string) (string, error) {
	m.mu.RLock()
	user, exists := m.users[username]
	m.mu.RUnlock()

	if !exists {
		return "", ErrInvalidCredentials
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return "", ErrInvalidCredentials
	}

	// Generate token
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", fmt.Errorf("failed to generate token: %w", err)
	}

	tokenValue := base64.URLEncoding.EncodeToString(tokenBytes)

	m.mu.Lock()
	m.tokens[tokenValue] = &Token{
		Value:     tokenValue,
		Username:  username,
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}
	m.mu.Unlock()

	return tokenValue, nil
}

// ValidateToken checks if a token is valid
func (m *Manager) ValidateToken(tokenValue string) (*User, error) {
	m.mu.RLock()
	token, exists := m.tokens[tokenValue]
	m.mu.RUnlock()

	if !exists {
		return nil, ErrInvalidToken
	}

	if time.Now().After(token.ExpiresAt) {
		m.mu.Lock()
		delete(m.tokens, tokenValue)
		m.mu.Unlock()
		return nil, ErrInvalidToken
	}

	m.mu.RLock()
	user, exists := m.users[token.Username]
	m.mu.RUnlock()

	if !exists {
		return nil, ErrUserNotFound
	}

	return user, nil
}

// InvalidateToken removes a token (logout)
func (m *Manager) InvalidateToken(tokenValue string) {
	m.mu.Lock()
	delete(m.tokens, tokenValue)
	m.mu.Unlock()
}

// HasUsers returns true if any users are configured
func (m *Manager) HasUsers() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.users) > 0
}

// UserCount returns the number of configured users
func (m *Manager) UserCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.users)
}
