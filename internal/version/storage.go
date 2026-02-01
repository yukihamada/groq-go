package version

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Storage handles persistence of version metadata
type Storage struct {
	dir string
	mu  sync.RWMutex
}

// NewStorage creates a new storage instance
func NewStorage(dir string) (*Storage, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	return &Storage{dir: dir}, nil
}

// Save persists a version to disk
func (s *Storage) Save(v *AgentVersion) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	versionDir := filepath.Join(s.dir, v.ID)
	if err := os.MkdirAll(versionDir, 0755); err != nil {
		return fmt.Errorf("failed to create version dir: %w", err)
	}

	metaPath := filepath.Join(versionDir, "meta.json")
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal version: %w", err)
	}

	if err := os.WriteFile(metaPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write version: %w", err)
	}

	return nil
}

// Load loads a version from disk
func (s *Storage) Load(id string) (*AgentVersion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	metaPath := filepath.Join(s.dir, id, "meta.json")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read version: %w", err)
	}

	var v AgentVersion
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("failed to unmarshal version: %w", err)
	}

	return &v, nil
}

// LoadAll loads all versions from disk
func (s *Storage) LoadAll() ([]*AgentVersion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read versions dir: %w", err)
	}

	var versions []*AgentVersion
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		metaPath := filepath.Join(s.dir, entry.Name(), "meta.json")
		data, err := os.ReadFile(metaPath)
		if err != nil {
			continue // Skip invalid entries
		}

		var v AgentVersion
		if err := json.Unmarshal(data, &v); err != nil {
			continue // Skip invalid entries
		}

		versions = append(versions, &v)
	}

	return versions, nil
}

// Delete removes a version from disk
func (s *Storage) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	versionDir := filepath.Join(s.dir, id)
	if err := os.RemoveAll(versionDir); err != nil {
		return fmt.Errorf("failed to delete version: %w", err)
	}

	return nil
}

// Exists checks if a version exists
func (s *Storage) Exists(id string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	metaPath := filepath.Join(s.dir, id, "meta.json")
	_, err := os.Stat(metaPath)
	return err == nil
}
