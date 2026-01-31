package project

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Project represents a project workspace
type Project struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	RootPath    string    `json:"root_path"`
	Description string    `json:"description,omitempty"`
	Sessions    []string  `json:"sessions,omitempty"` // Session IDs associated with this project
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ProjectMeta contains project metadata for listing
type ProjectMeta struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	RootPath  string    `json:"root_path"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Manager manages projects
type Manager struct {
	mu         sync.RWMutex
	projects   map[string]*Project
	configPath string
	current    string // Current project ID
}

// NewManager creates a new project manager
func NewManager() (*Manager, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}

	configPath := filepath.Join(home, ".config", "groq-go", "projects.json")

	m := &Manager{
		projects:   make(map[string]*Project),
		configPath: configPath,
	}

	// Load existing projects
	if err := m.load(); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to load projects: %w", err)
	}

	return m, nil
}

func (m *Manager) load() error {
	data, err := os.ReadFile(m.configPath)
	if err != nil {
		return err
	}

	var config struct {
		Projects []*Project `json:"projects"`
		Current  string     `json:"current"`
	}

	if err := json.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("failed to parse projects config: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, p := range config.Projects {
		m.projects[p.ID] = p
	}
	m.current = config.Current

	return nil
}

func (m *Manager) save() error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var projects []*Project
	for _, p := range m.projects {
		projects = append(projects, p)
	}

	// Sort by name
	sort.Slice(projects, func(i, j int) bool {
		return projects[i].Name < projects[j].Name
	})

	config := struct {
		Projects []*Project `json:"projects"`
		Current  string     `json:"current"`
	}{
		Projects: projects,
		Current:  m.current,
	}

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal projects: %w", err)
	}

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(m.configPath), 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	if err := os.WriteFile(m.configPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write projects file: %w", err)
	}

	return nil
}

// Create creates a new project
func (m *Manager) Create(name, rootPath, description string) (*Project, error) {
	m.mu.Lock()

	// Check for duplicate name
	for _, p := range m.projects {
		if p.Name == name {
			m.mu.Unlock()
			return nil, fmt.Errorf("project with name '%s' already exists", name)
		}
	}

	id := fmt.Sprintf("proj-%d", time.Now().UnixNano())
	now := time.Now()

	project := &Project{
		ID:          id,
		Name:        name,
		RootPath:    rootPath,
		Description: description,
		Sessions:    []string{},
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	m.projects[id] = project
	m.mu.Unlock()

	if err := m.save(); err != nil {
		return nil, err
	}

	return project, nil
}

// Get returns a project by ID
func (m *Manager) Get(id string) (*Project, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	p, exists := m.projects[id]
	if !exists {
		return nil, fmt.Errorf("project not found: %s", id)
	}

	return p, nil
}

// Update updates a project
func (m *Manager) Update(id string, name, rootPath, description string) error {
	m.mu.Lock()

	p, exists := m.projects[id]
	if !exists {
		m.mu.Unlock()
		return fmt.Errorf("project not found: %s", id)
	}

	if name != "" {
		p.Name = name
	}
	if rootPath != "" {
		p.RootPath = rootPath
	}
	if description != "" {
		p.Description = description
	}
	p.UpdatedAt = time.Now()

	m.mu.Unlock()

	return m.save()
}

// Delete deletes a project
func (m *Manager) Delete(id string) error {
	m.mu.Lock()
	delete(m.projects, id)
	if m.current == id {
		m.current = ""
	}
	m.mu.Unlock()

	return m.save()
}

// List returns all projects
func (m *Manager) List() []*ProjectMeta {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var list []*ProjectMeta
	for _, p := range m.projects {
		list = append(list, &ProjectMeta{
			ID:        p.ID,
			Name:      p.Name,
			RootPath:  p.RootPath,
			UpdatedAt: p.UpdatedAt,
		})
	}

	// Sort by updated time, most recent first
	sort.Slice(list, func(i, j int) bool {
		return list[i].UpdatedAt.After(list[j].UpdatedAt)
	})

	return list
}

// SetCurrent sets the current project
func (m *Manager) SetCurrent(id string) error {
	m.mu.Lock()
	if _, exists := m.projects[id]; !exists && id != "" {
		m.mu.Unlock()
		return fmt.Errorf("project not found: %s", id)
	}
	m.current = id
	m.mu.Unlock()

	return m.save()
}

// Current returns the current project
func (m *Manager) Current() *Project {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.current == "" {
		return nil
	}

	return m.projects[m.current]
}

// AddSession adds a session to a project
func (m *Manager) AddSession(projectID, sessionID string) error {
	m.mu.Lock()

	p, exists := m.projects[projectID]
	if !exists {
		m.mu.Unlock()
		return fmt.Errorf("project not found: %s", projectID)
	}

	// Check if session already exists
	for _, s := range p.Sessions {
		if s == sessionID {
			m.mu.Unlock()
			return nil // Already exists
		}
	}

	p.Sessions = append(p.Sessions, sessionID)
	p.UpdatedAt = time.Now()
	m.mu.Unlock()

	return m.save()
}

// RemoveSession removes a session from a project
func (m *Manager) RemoveSession(projectID, sessionID string) error {
	m.mu.Lock()

	p, exists := m.projects[projectID]
	if !exists {
		m.mu.Unlock()
		return fmt.Errorf("project not found: %s", projectID)
	}

	for i, s := range p.Sessions {
		if s == sessionID {
			p.Sessions = append(p.Sessions[:i], p.Sessions[i+1:]...)
			p.UpdatedAt = time.Now()
			break
		}
	}

	m.mu.Unlock()
	return m.save()
}
