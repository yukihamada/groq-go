package version

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// BuildVersion compiles the version's binary
func (m *Manager) BuildVersion(ctx context.Context, id string) error {
	m.mu.Lock()
	v, ok := m.versions[id]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("version %s not found", id)
	}

	if !v.CanBuild() && v.Status != StatusReady {
		m.mu.Unlock()
		return fmt.Errorf("version cannot be built (status: %s)", v.Status)
	}

	v.Status = StatusBuilding
	v.Error = ""
	m.storage.Save(v)
	m.mu.Unlock()

	// Do the build without holding the lock
	err := m.doBuild(ctx, v)

	m.mu.Lock()
	defer m.mu.Unlock()

	if err != nil {
		v.Status = StatusFailed
		v.Error = err.Error()
		m.storage.Save(v)
		return err
	}

	v.Status = StatusReady
	v.BuildAt = time.Now()
	v.Error = ""

	// Update commit hash after build
	if m.selfimprove != nil {
		v.CommitHash = m.getCurrentCommit(ctx)
	}

	return m.storage.Save(v)
}

func (m *Manager) doBuild(ctx context.Context, v *AgentVersion) error {
	repoDir := m.GetRepoDir()
	if repoDir == "" {
		return fmt.Errorf("repo not initialized")
	}

	// Checkout the version's branch
	if err := runGit(ctx, repoDir, "checkout", v.Branch); err != nil {
		return fmt.Errorf("failed to checkout branch %s: %w", v.Branch, err)
	}

	// Build the binary
	cmd := exec.CommandContext(ctx, "go", "build", "-o", v.BinaryPath, ".")
	cmd.Dir = repoDir
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("build failed: %s - %w", string(output), err)
	}

	// Verify binary exists and is executable
	info, err := os.Stat(v.BinaryPath)
	if err != nil {
		return fmt.Errorf("binary not created: %w", err)
	}
	if info.Size() == 0 {
		return fmt.Errorf("binary is empty")
	}

	return nil
}

// RebuildVersion rebuilds an existing version (for after code changes)
func (m *Manager) RebuildVersion(ctx context.Context, id string) error {
	m.mu.Lock()
	v, ok := m.versions[id]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("version %s not found", id)
	}

	// Stop if running
	if v.IsActive() {
		if err := m.stopVersionLocked(v); err != nil {
			m.mu.Unlock()
			return fmt.Errorf("failed to stop version: %w", err)
		}
	}
	m.mu.Unlock()

	return m.BuildVersion(ctx, id)
}

// Helper functions for git operations

func runGit(ctx context.Context, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s failed: %s - %w", args[0], string(output), err)
	}
	return nil
}

func runGitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s failed: %w", args[0], err)
	}
	return strings.TrimSpace(string(output)), nil
}

// RunGitCmd is an exported version of runGit for use by other packages
func RunGitCmd(ctx context.Context, dir string, args ...string) error {
	return runGit(ctx, dir, args...)
}
