package storage

import (
	"context"
	"time"

	"groq-go/internal/client"
)

// Session represents a conversation session
type Session struct {
	ID        string           `json:"id"`
	Title     string           `json:"title"`
	Messages  []client.Message `json:"messages"`
	Files     []FileEntry      `json:"files,omitempty"`
	CreatedAt time.Time        `json:"created_at"`
	UpdatedAt time.Time        `json:"updated_at"`
}

// FileEntry represents a file in a session
type FileEntry struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Content string `json:"content"`
}

// SessionMeta represents session metadata for listing
type SessionMeta struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// SharedConversation represents a shared conversation link
type SharedConversation struct {
	ShareID   string           `json:"share_id"`
	SessionID string           `json:"session_id"`
	Title     string           `json:"title"`
	Messages  []client.Message `json:"messages"`
	CreatedAt time.Time        `json:"created_at"`
	ExpiresAt time.Time        `json:"expires_at,omitempty"`
	ViewCount int              `json:"view_count"`
}

// Storage defines the interface for session storage
type Storage interface {
	// SaveSession saves or updates a session
	SaveSession(ctx context.Context, session *Session) error

	// LoadSession loads a session by ID
	LoadSession(ctx context.Context, id string) (*Session, error)

	// ListSessions returns all session metadata
	ListSessions(ctx context.Context) ([]*SessionMeta, error)

	// DeleteSession deletes a session by ID
	DeleteSession(ctx context.Context, id string) error

	// SaveShare saves a shared conversation
	SaveShare(ctx context.Context, share *SharedConversation) error

	// LoadShare loads a shared conversation by share ID
	LoadShare(ctx context.Context, shareID string) (*SharedConversation, error)

	// IncrementShareViewCount increments the view count for a share
	IncrementShareViewCount(ctx context.Context, shareID string) error

	// Close closes the storage
	Close() error
}
