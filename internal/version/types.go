package version

import (
	"time"
)

// Status represents the current state of an agent version
type Status string

const (
	StatusPending  Status = "pending"  // Created but not built
	StatusBuilding Status = "building" // Build in progress
	StatusReady    Status = "ready"    // Built and ready to start
	StatusRunning  Status = "running"  // Currently running
	StatusFailed   Status = "failed"   // Build or start failed
	StatusStopped  Status = "stopped"  // Was running, now stopped
)

// AgentVersion represents a version of the agent
type AgentVersion struct {
	ID          string    `json:"id"`           // Unique ID (uuid)
	Name        string    `json:"name"`         // User-facing name
	Branch      string    `json:"branch"`       // Git branch name
	CommitHash  string    `json:"commit_hash"`  // Git commit SHA
	BinaryPath  string    `json:"binary_path"`  // Path to built binary
	Port        int       `json:"port"`         // Running port (0 if not running)
	PID         int       `json:"pid"`          // Process ID (0 if not running)
	Status      Status    `json:"status"`       // Current status
	Description string    `json:"description"`  // Description of changes
	Error       string    `json:"error"`        // Error message if failed
	CreatedAt   time.Time `json:"created_at"`   // When version was created
	BuildAt     time.Time `json:"built_at"`     // When version was built
	StartedAt   time.Time `json:"started_at"`   // When version was started
}

// IsActive returns true if the version process is running
func (v *AgentVersion) IsActive() bool {
	return v.Status == StatusRunning && v.PID > 0
}

// CanStart returns true if the version can be started
func (v *AgentVersion) CanStart() bool {
	return v.Status == StatusReady || v.Status == StatusStopped
}

// CanBuild returns true if the version can be built
func (v *AgentVersion) CanBuild() bool {
	return v.Status == StatusPending || v.Status == StatusFailed
}
