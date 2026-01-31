package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// FileStorage implements Storage using JSON files
type FileStorage struct {
	dir string
	mu  sync.RWMutex
}

// NewFileStorage creates a new file-based storage
func NewFileStorage(dir string) (*FileStorage, error) {
	// Create directory if it doesn't exist
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create storage directory: %w", err)
	}

	return &FileStorage{dir: dir}, nil
}

// DefaultStorageDir returns the default storage directory
func DefaultStorageDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".config", "groq-go", "sessions")
}

func (s *FileStorage) sessionPath(id string) string {
	return filepath.Join(s.dir, id+".json")
}

// SaveSession saves or updates a session
func (s *FileStorage) SaveSession(ctx context.Context, session *Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Set timestamps
	now := time.Now()
	if session.CreatedAt.IsZero() {
		session.CreatedAt = now
	}
	session.UpdatedAt = now

	// Generate title from first user message if not set
	if session.Title == "" && len(session.Messages) > 0 {
		for _, msg := range session.Messages {
			if msg.Role == "user" {
				// Content can be string or []ContentPart
				if title, ok := msg.Content.(string); ok {
					if len(title) > 50 {
						title = title[:50] + "..."
					}
					session.Title = title
				}
				break
			}
		}
	}

	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal session: %w", err)
	}

	if err := os.WriteFile(s.sessionPath(session.ID), data, 0644); err != nil {
		return fmt.Errorf("failed to write session file: %w", err)
	}

	return nil
}

// LoadSession loads a session by ID
func (s *FileStorage) LoadSession(ctx context.Context, id string) (*Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, err := os.ReadFile(s.sessionPath(id))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read session file: %w", err)
	}

	var session Session
	if err := json.Unmarshal(data, &session); err != nil {
		return nil, fmt.Errorf("failed to unmarshal session: %w", err)
	}

	return &session, nil
}

// ListSessions returns all session metadata
func (s *FileStorage) ListSessions(ctx context.Context) ([]*SessionMeta, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read storage directory: %w", err)
	}

	var sessions []*SessionMeta

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		data, err := os.ReadFile(filepath.Join(s.dir, entry.Name()))
		if err != nil {
			continue
		}

		var session Session
		if err := json.Unmarshal(data, &session); err != nil {
			continue
		}

		sessions = append(sessions, &SessionMeta{
			ID:        session.ID,
			Title:     session.Title,
			CreatedAt: session.CreatedAt,
			UpdatedAt: session.UpdatedAt,
		})
	}

	// Sort by updated time, most recent first
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})

	return sessions, nil
}

// DeleteSession deletes a session by ID
func (s *FileStorage) DeleteSession(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := s.sessionPath(id)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete session file: %w", err)
	}

	return nil
}

func (s *FileStorage) sharePath(id string) string {
	return filepath.Join(s.dir, "shares", id+".json")
}

// SaveShare saves a shared conversation
func (s *FileStorage) SaveShare(ctx context.Context, share *SharedConversation) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Create shares directory if it doesn't exist
	sharesDir := filepath.Join(s.dir, "shares")
	if err := os.MkdirAll(sharesDir, 0755); err != nil {
		return fmt.Errorf("failed to create shares directory: %w", err)
	}

	data, err := json.MarshalIndent(share, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal share: %w", err)
	}

	if err := os.WriteFile(s.sharePath(share.ShareID), data, 0644); err != nil {
		return fmt.Errorf("failed to write share file: %w", err)
	}

	return nil
}

// LoadShare loads a shared conversation by share ID
func (s *FileStorage) LoadShare(ctx context.Context, shareID string) (*SharedConversation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, err := os.ReadFile(s.sharePath(shareID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read share file: %w", err)
	}

	var share SharedConversation
	if err := json.Unmarshal(data, &share); err != nil {
		return nil, fmt.Errorf("failed to unmarshal share: %w", err)
	}

	return &share, nil
}

// IncrementShareViewCount increments the view count for a share
func (s *FileStorage) IncrementShareViewCount(ctx context.Context, shareID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.sharePath(shareID))
	if err != nil {
		return fmt.Errorf("failed to read share file: %w", err)
	}

	var share SharedConversation
	if err := json.Unmarshal(data, &share); err != nil {
		return fmt.Errorf("failed to unmarshal share: %w", err)
	}

	share.ViewCount++

	newData, err := json.MarshalIndent(share, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal share: %w", err)
	}

	if err := os.WriteFile(s.sharePath(shareID), newData, 0644); err != nil {
		return fmt.Errorf("failed to write share file: %w", err)
	}

	return nil
}

// Close closes the storage (no-op for file storage)
func (s *FileStorage) Close() error {
	return nil
}
