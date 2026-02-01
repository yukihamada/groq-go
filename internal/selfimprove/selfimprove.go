package selfimprove

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Manager handles self-improvement operations
type Manager struct {
	repoDir         string
	repoURL         string
	githubToken     string
	mu              sync.Mutex
	history         []Commit
	lastKnownGood   string // Last known working commit hash
	safeCommitFile  string // File to persist last known good commit
}

// Commit represents a git commit
type Commit struct {
	Hash      string    `json:"hash"`
	Message   string    `json:"message"`
	Timestamp time.Time `json:"timestamp"`
}

// NewManager creates a new self-improvement manager
func NewManager() (*Manager, error) {
	githubToken := os.Getenv("GITHUB_TOKEN")
	repoURL := os.Getenv("SELF_REPO_URL")
	if repoURL == "" {
		repoURL = "https://github.com/yukihamada/groq-go.git"
	}

	// Working directory for the repo
	home, _ := os.UserHomeDir()
	repoDir := filepath.Join(home, ".groq-go-repo")
	safeCommitFile := filepath.Join(home, ".config", "groq-go", "last_known_good")

	// Ensure config directory exists
	os.MkdirAll(filepath.Dir(safeCommitFile), 0755)

	m := &Manager{
		repoDir:        repoDir,
		repoURL:        repoURL,
		githubToken:    githubToken,
		history:        make([]Commit, 0),
		safeCommitFile: safeCommitFile,
	}

	// Load last known good commit
	if data, err := os.ReadFile(safeCommitFile); err == nil {
		m.lastKnownGood = strings.TrimSpace(string(data))
	}

	return m, nil
}

// Init initializes the repository
func (m *Manager) Init(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if already cloned
	if _, err := os.Stat(filepath.Join(m.repoDir, ".git")); err == nil {
		// Pull latest
		return m.runGit(ctx, "pull", "origin", "main")
	}

	// Clone the repository
	url := m.repoURL
	if m.githubToken != "" {
		// Insert token into URL for auth
		url = strings.Replace(url, "https://", "https://"+m.githubToken+"@", 1)
	}

	if err := os.MkdirAll(filepath.Dir(m.repoDir), 0755); err != nil {
		return err
	}

	cmd := exec.CommandContext(ctx, "git", "clone", url, m.repoDir)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("clone failed: %s - %w", string(output), err)
	}

	// Configure git
	m.runGit(ctx, "config", "user.email", "ai@groq-go.dev")
	m.runGit(ctx, "config", "user.name", "groq-go AI")

	// Load commit history
	m.loadHistory(ctx)

	return nil
}

// GetRepoDir returns the repository directory
func (m *Manager) GetRepoDir() string {
	return m.repoDir
}

// ReadFile reads a file from the repository
func (m *Manager) ReadFile(ctx context.Context, path string) (string, error) {
	fullPath := filepath.Join(m.repoDir, path)
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// WriteFile writes a file to the repository
func (m *Manager) WriteFile(ctx context.Context, path, content string) error {
	fullPath := filepath.Join(m.repoDir, path)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return err
	}
	return os.WriteFile(fullPath, []byte(content), 0644)
}

// ListFiles lists files in the repository
func (m *Manager) ListFiles(ctx context.Context, pattern string) ([]string, error) {
	var files []string
	err := filepath.Walk(m.repoDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}

		relPath, _ := filepath.Rel(m.repoDir, path)
		if pattern == "" || strings.Contains(relPath, pattern) {
			files = append(files, relPath)
		}
		return nil
	})
	return files, err
}

// Commit commits changes with a message
func (m *Manager) Commit(ctx context.Context, message string) (*Commit, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Stage all changes
	if err := m.runGit(ctx, "add", "-A"); err != nil {
		return nil, err
	}

	// Check if there are changes to commit
	output, _ := exec.CommandContext(ctx, "git", "-C", m.repoDir, "status", "--porcelain").Output()
	if len(output) == 0 {
		return nil, fmt.Errorf("no changes to commit")
	}

	// Commit
	if err := m.runGit(ctx, "commit", "-m", message); err != nil {
		return nil, err
	}

	// Get commit hash
	hashOutput, err := exec.CommandContext(ctx, "git", "-C", m.repoDir, "rev-parse", "HEAD").Output()
	if err != nil {
		return nil, err
	}

	commit := &Commit{
		Hash:      strings.TrimSpace(string(hashOutput)),
		Message:   message,
		Timestamp: time.Now(),
	}

	m.history = append(m.history, *commit)
	m.saveHistory()

	return commit, nil
}

// Push pushes changes to remote
func (m *Manager) Push(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.runGit(ctx, "push", "origin", "main")
}

// Rollback rolls back to a previous commit
func (m *Manager) Rollback(ctx context.Context, commitHash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Create a revert commit
	if err := m.runGit(ctx, "revert", "--no-commit", "HEAD"); err != nil {
		// If revert fails, try reset
		if err := m.runGit(ctx, "reset", "--hard", commitHash); err != nil {
			return err
		}
	}

	return nil
}

// RollbackToLast rolls back to the previous commit
func (m *Manager) RollbackToLast(ctx context.Context) error {
	if len(m.history) < 2 {
		return fmt.Errorf("no previous commit to rollback to")
	}

	prevCommit := m.history[len(m.history)-2]
	return m.Rollback(ctx, prevCommit.Hash)
}

// GetHistory returns commit history
func (m *Manager) GetHistory() []Commit {
	return m.history
}

// GetStatus returns git status
func (m *Manager) GetStatus(ctx context.Context) (string, error) {
	output, err := exec.CommandContext(ctx, "git", "-C", m.repoDir, "status").Output()
	if err != nil {
		return "", err
	}
	return string(output), nil
}

// GetDiff returns git diff
func (m *Manager) GetDiff(ctx context.Context) (string, error) {
	output, err := exec.CommandContext(ctx, "git", "-C", m.repoDir, "diff").Output()
	if err != nil {
		return "", err
	}
	return string(output), nil
}

func (m *Manager) runGit(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", m.repoDir}, args...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s failed: %s - %w", args[0], string(output), err)
	}
	return nil
}

func (m *Manager) loadHistory(ctx context.Context) {
	// Load last 10 commits
	output, err := exec.CommandContext(ctx, "git", "-C", m.repoDir, "log", "--oneline", "-10").Output()
	if err != nil {
		return
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	for _, line := range lines {
		parts := strings.SplitN(line, " ", 2)
		if len(parts) == 2 {
			m.history = append(m.history, Commit{
				Hash:    parts[0],
				Message: parts[1],
			})
		}
	}
}

func (m *Manager) saveHistory() {
	// Keep last 20 commits
	if len(m.history) > 20 {
		m.history = m.history[len(m.history)-20:]
	}
}

// ToJSON returns the history as JSON
func (m *Manager) ToJSON() string {
	data, _ := json.MarshalIndent(m.history, "", "  ")
	return string(data)
}

// VerifyBuild tests if the code compiles successfully
func (m *Manager) VerifyBuild(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "go", "build", "-o", "/dev/null", ".")
	cmd.Dir = m.repoDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("build verification failed: %s - %w", string(output), err)
	}
	return nil
}

// SafePush pushes only if the code builds successfully
func (m *Manager) SafePush(ctx context.Context) error {
	// First verify the build
	if err := m.VerifyBuild(ctx); err != nil {
		return fmt.Errorf("cannot push: %w", err)
	}

	// Push to remote
	if err := m.Push(ctx); err != nil {
		return err
	}

	// Mark as last known good
	return m.MarkAsGood(ctx)
}

// MarkAsGood marks the current commit as last known good
func (m *Manager) MarkAsGood(ctx context.Context) error {
	output, err := exec.CommandContext(ctx, "git", "-C", m.repoDir, "rev-parse", "HEAD").Output()
	if err != nil {
		return err
	}
	hash := strings.TrimSpace(string(output))
	m.lastKnownGood = hash
	return os.WriteFile(m.safeCommitFile, []byte(hash), 0644)
}

// GetLastKnownGood returns the last known good commit hash
func (m *Manager) GetLastKnownGood() string {
	return m.lastKnownGood
}

// RollbackToCommit rolls back to a specific commit by hash
func (m *Manager) RollbackToCommit(ctx context.Context, hash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Verify the commit exists
	cmd := exec.CommandContext(ctx, "git", "-C", m.repoDir, "cat-file", "-t", hash)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("commit %s not found", hash)
	}

	// Reset to that commit
	return m.runGit(ctx, "reset", "--hard", hash)
}

// RollbackToSafe rolls back to the last known good commit
func (m *Manager) RollbackToSafe(ctx context.Context) error {
	if m.lastKnownGood == "" {
		return fmt.Errorf("no known good commit saved - use 'fly_rollback' for Fly.io rollback")
	}
	return m.RollbackToCommit(ctx, m.lastKnownGood)
}

// GetFlyRollbackInfo returns Fly.io rollback instructions
func (m *Manager) GetFlyRollbackInfo(ctx context.Context) (string, error) {
	// Try to get release list from fly
	cmd := exec.CommandContext(ctx, "flyctl", "releases", "-a", "groq-go-yuki")
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Return manual instructions if flyctl not available
		return `## Fly.io Manual Rollback

If all else fails, you can rollback using Fly.io directly:

1. SSH into any machine with flyctl installed
2. Run: flyctl releases -a groq-go-yuki
3. Find a working version number (e.g., v42)
4. Run: flyctl releases rollback v42 -a groq-go-yuki

Or via Fly.io dashboard:
1. Go to https://fly.io/apps/groq-go-yuki
2. Click "Releases"
3. Click "Rollback" on a working version

This will immediately deploy the previous working version.`, nil
	}

	return fmt.Sprintf("## Fly.io Releases\n\n%s\n\nTo rollback: flyctl releases rollback <version> -a groq-go-yuki", string(output)), nil
}
