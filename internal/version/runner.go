package version

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// StartVersion starts a version on an available port
func (m *Manager) StartVersion(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	v, ok := m.versions[id]
	if !ok {
		return fmt.Errorf("version %s not found", id)
	}

	if v.IsActive() {
		return fmt.Errorf("version is already running on port %d", v.Port)
	}

	if !v.CanStart() {
		return fmt.Errorf("version cannot be started (status: %s)", v.Status)
	}

	// Verify binary exists
	if _, err := os.Stat(v.BinaryPath); err != nil {
		return fmt.Errorf("binary not found: %w", err)
	}

	// Allocate port
	port := m.AllocatePort()
	if port == 0 {
		return fmt.Errorf("no available ports (all %d-%d in use)", BasePort, MaxPort)
	}

	// Start the process
	addr := fmt.Sprintf(":%d", port)
	cmd := exec.Command(v.BinaryPath, "-web", "-addr", addr)

	// Set up process group so we can kill children too
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	// Redirect output to log files
	versionDir := m.baseDir + "/" + id
	logFile, err := os.OpenFile(versionDir+"/output.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err == nil {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start version: %w", err)
	}

	v.PID = cmd.Process.Pid
	v.Port = port
	v.Status = StatusRunning
	v.StartedAt = time.Now()
	v.Error = ""

	// Save state
	if err := m.storage.Save(v); err != nil {
		// Try to kill the process if we can't save state
		cmd.Process.Kill()
		return fmt.Errorf("failed to save version state: %w", err)
	}

	// Monitor process in background
	go m.monitorProcess(v, cmd)

	return nil
}

// StopVersion stops a running version
func (m *Manager) StopVersion(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	v, ok := m.versions[id]
	if !ok {
		return fmt.Errorf("version %s not found", id)
	}

	if !v.IsActive() {
		return fmt.Errorf("version is not running")
	}

	return m.stopVersionLocked(v)
}

// monitorProcess monitors a running version process
func (m *Manager) monitorProcess(v *AgentVersion, cmd *exec.Cmd) {
	// Wait for process to exit
	err := cmd.Wait()

	m.mu.Lock()
	defer m.mu.Unlock()

	// Update status
	if v.Status == StatusRunning {
		if err != nil {
			v.Status = StatusFailed
			v.Error = fmt.Sprintf("process exited: %v", err)
		} else {
			v.Status = StatusStopped
		}
		v.PID = 0
		v.Port = 0
		m.storage.Save(v)
	}
}

// GetVersionLogs returns the log output of a version
func (m *Manager) GetVersionLogs(id string, lines int) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	v, ok := m.versions[id]
	if !ok {
		return "", fmt.Errorf("version %s not found", id)
	}

	logPath := m.baseDir + "/" + v.ID + "/output.log"
	data, err := os.ReadFile(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "(no logs)", nil
		}
		return "", err
	}

	content := string(data)
	if lines > 0 && len(content) > lines*100 {
		// Rough approximation: take last N*100 bytes
		content = content[len(content)-lines*100:]
	}

	return content, nil
}

// RestartVersion stops and starts a version
func (m *Manager) RestartVersion(ctx context.Context, id string) error {
	// Stop if running
	m.mu.RLock()
	v, ok := m.versions[id]
	m.mu.RUnlock()

	if !ok {
		return fmt.Errorf("version %s not found", id)
	}

	if v.IsActive() {
		if err := m.StopVersion(ctx, id); err != nil {
			return fmt.Errorf("failed to stop: %w", err)
		}
		// Give it a moment to stop
		time.Sleep(500 * time.Millisecond)
	}

	return m.StartVersion(ctx, id)
}

// CheckHealth checks if a version is responding
func (m *Manager) CheckHealth(ctx context.Context, id string) (bool, error) {
	m.mu.RLock()
	v, ok := m.versions[id]
	m.mu.RUnlock()

	if !ok {
		return false, fmt.Errorf("version %s not found", id)
	}

	if !v.IsActive() {
		return false, nil
	}

	// Check if process is still running
	proc, err := os.FindProcess(v.PID)
	if err != nil {
		return false, nil
	}

	// Send signal 0 to check if process exists
	err = proc.Signal(syscall.Signal(0))
	return err == nil, nil
}
