package version

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"

	"groq-go/internal/selfimprove"
)

const (
	// MaxVersions is the maximum number of versions to keep
	MaxVersions = 5
	// BasePort is the starting port for version instances
	BasePort = 8081
	// MaxPort is the maximum port for version instances
	MaxPort = 8090
)

// Manager manages agent versions
type Manager struct {
	baseDir     string                    // ~/.config/groq-go/versions
	versions    map[string]*AgentVersion  // All versions by ID
	selfimprove *selfimprove.Manager      // For git operations
	mu          sync.RWMutex
	storage     *Storage
}

// NewManager creates a new version manager
func NewManager(sim *selfimprove.Manager) (*Manager, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home dir: %w", err)
	}

	baseDir := filepath.Join(home, ".config", "groq-go", "versions")
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create versions dir: %w", err)
	}

	storage, err := NewStorage(baseDir)
	if err != nil {
		return nil, fmt.Errorf("failed to create storage: %w", err)
	}

	m := &Manager{
		baseDir:     baseDir,
		versions:    make(map[string]*AgentVersion),
		selfimprove: sim,
		storage:     storage,
	}

	// Load existing versions from storage
	versions, err := storage.LoadAll()
	if err != nil {
		return nil, fmt.Errorf("failed to load versions: %w", err)
	}
	for _, v := range versions {
		// Reset running status on startup (process may have died)
		if v.Status == StatusRunning {
			v.Status = StatusStopped
			v.PID = 0
			v.Port = 0
		}
		m.versions[v.ID] = v
	}

	return m, nil
}

// CreateVersion creates a new version with a git branch
func (m *Manager) CreateVersion(ctx context.Context, name, description string) (*AgentVersion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check version limit
	if len(m.versions) >= MaxVersions {
		return nil, fmt.Errorf("maximum versions (%d) reached, delete some first", MaxVersions)
	}

	// Generate unique ID and branch name
	id := uuid.New().String()[:8]
	branch := fmt.Sprintf("version-%s-%s", id, sanitizeName(name))

	// Create version directory
	versionDir := filepath.Join(m.baseDir, id)
	if err := os.MkdirAll(versionDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create version dir: %w", err)
	}

	// Create git branch if selfimprove is available
	var commitHash string
	if m.selfimprove != nil {
		if err := m.createBranch(ctx, branch); err != nil {
			os.RemoveAll(versionDir)
			return nil, fmt.Errorf("failed to create branch: %w", err)
		}
		commitHash = m.getCurrentCommit(ctx)
	}

	version := &AgentVersion{
		ID:          id,
		Name:        name,
		Branch:      branch,
		CommitHash:  commitHash,
		BinaryPath:  filepath.Join(versionDir, "groq-go"),
		Status:      StatusPending,
		Description: description,
		CreatedAt:   time.Now(),
	}

	m.versions[id] = version

	// Persist
	if err := m.storage.Save(version); err != nil {
		return nil, fmt.Errorf("failed to save version: %w", err)
	}

	return version, nil
}

// GetVersion returns a version by ID
func (m *Manager) GetVersion(id string) (*AgentVersion, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	v, ok := m.versions[id]
	return v, ok
}

// ListVersions returns all versions
func (m *Manager) ListVersions() []*AgentVersion {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*AgentVersion, 0, len(m.versions))
	for _, v := range m.versions {
		result = append(result, v)
	}
	return result
}

// DeleteVersion removes a version
func (m *Manager) DeleteVersion(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	v, ok := m.versions[id]
	if !ok {
		return fmt.Errorf("version %s not found", id)
	}

	// Stop if running
	if v.IsActive() {
		if err := m.stopVersionLocked(v); err != nil {
			return fmt.Errorf("failed to stop version: %w", err)
		}
	}

	// Delete branch
	if m.selfimprove != nil && v.Branch != "" {
		m.deleteBranch(ctx, v.Branch)
	}

	// Delete version directory
	versionDir := filepath.Join(m.baseDir, id)
	os.RemoveAll(versionDir)

	// Remove from storage
	m.storage.Delete(id)

	delete(m.versions, id)
	return nil
}

// AllocatePort finds an available port
func (m *Manager) AllocatePort() int {
	usedPorts := make(map[int]bool)
	for _, v := range m.versions {
		if v.Port > 0 {
			usedPorts[v.Port] = true
		}
	}

	for port := BasePort; port <= MaxPort; port++ {
		if !usedPorts[port] {
			return port
		}
	}
	return 0 // No port available
}

// GetRepoDir returns the selfimprove repo directory
func (m *Manager) GetRepoDir() string {
	if m.selfimprove != nil {
		return m.selfimprove.GetRepoDir()
	}
	return ""
}

// GetSelfImprove returns the selfimprove manager
func (m *Manager) GetSelfImprove() *selfimprove.Manager {
	return m.selfimprove
}

// UpdateVersion updates a version and persists changes
func (m *Manager) UpdateVersion(v *AgentVersion) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.storage.Save(v)
}

// Helper functions

func (m *Manager) createBranch(ctx context.Context, branch string) error {
	repoDir := m.selfimprove.GetRepoDir()
	if repoDir == "" {
		return fmt.Errorf("repo not initialized")
	}
	return runGit(ctx, repoDir, "checkout", "-b", branch)
}

func (m *Manager) deleteBranch(ctx context.Context, branch string) error {
	repoDir := m.selfimprove.GetRepoDir()
	if repoDir == "" {
		return nil
	}
	// Switch to main first if on the branch being deleted
	runGit(ctx, repoDir, "checkout", "main")
	return runGit(ctx, repoDir, "branch", "-D", branch)
}

func (m *Manager) getCurrentCommit(ctx context.Context) string {
	repoDir := m.selfimprove.GetRepoDir()
	if repoDir == "" {
		return ""
	}
	output, err := runGitOutput(ctx, repoDir, "rev-parse", "HEAD")
	if err != nil {
		return ""
	}
	return output
}

func (m *Manager) stopVersionLocked(v *AgentVersion) error {
	if v.PID <= 0 {
		return nil
	}
	proc, err := os.FindProcess(v.PID)
	if err != nil {
		return err
	}
	proc.Kill()
	v.Status = StatusStopped
	v.PID = 0
	v.Port = 0
	return m.storage.Save(v)
}

func sanitizeName(name string) string {
	result := make([]byte, 0, len(name))
	for _, c := range name {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' {
			result = append(result, byte(c))
		} else if c == ' ' {
			result = append(result, '-')
		}
	}
	if len(result) > 20 {
		result = result[:20]
	}
	return string(result)
}
