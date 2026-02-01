package credits

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Manager handles credit management for users
type Manager struct {
	dataDir string
	users   map[string]*UserCredits
	mu      sync.RWMutex
}

// UserCredits represents a user's credit balance
type UserCredits struct {
	UserID       string    `json:"user_id"`
	Email        string    `json:"email"`
	Balance      int       `json:"balance"`       // Credits remaining
	TotalUsed    int       `json:"total_used"`    // Total credits used
	TotalBought  int       `json:"total_bought"`  // Total credits purchased
	FreeCredits  int       `json:"free_credits"`  // Free credits given
	LastUsed     time.Time `json:"last_used"`
	CreatedAt    time.Time `json:"created_at"`
	Transactions []Transaction `json:"transactions"`
}

// Transaction represents a credit transaction
type Transaction struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"` // "use", "buy", "free", "refund"
	Amount    int       `json:"amount"`
	Balance   int       `json:"balance_after"`
	Model     string    `json:"model,omitempty"`
	Tokens    int       `json:"tokens,omitempty"`
	Note      string    `json:"note,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// CreditCost defines cost per model
var CreditCost = map[string]int{
	// Claude models (expensive)
	"claude-sonnet-4-20250514":    5,
	"claude-3-5-sonnet-20241022":  5,
	"claude-3-5-haiku-20241022":   2,
	"claude-3-opus-20240229":      10,
	// Groq models (cheap)
	"llama-3.3-70b-versatile":     1,
	"llama-3.1-8b-instant":        1,
	"llama-3.2-90b-vision-preview": 2,
	"mixtral-8x7b-32768":          1,
	// OpenAI models
	"gpt-4o":                      5,
	"gpt-4o-mini":                 2,
}

const (
	FreeCreditsForNewUser = 100
	DefaultDataDir        = ".config/groq-go/credits"
)

// NewManager creates a new credit manager
func NewManager() (*Manager, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	dataDir := filepath.Join(home, DefaultDataDir)
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, err
	}

	m := &Manager{
		dataDir: dataDir,
		users:   make(map[string]*UserCredits),
	}

	// Load existing users
	if err := m.loadAll(); err != nil {
		return nil, err
	}

	return m, nil
}

// GetOrCreateUser gets or creates a user's credit account
func (m *Manager) GetOrCreateUser(userID, email string) *UserCredits {
	m.mu.Lock()
	defer m.mu.Unlock()

	if user, exists := m.users[userID]; exists {
		return user
	}

	// Create new user with free credits
	user := &UserCredits{
		UserID:      userID,
		Email:       email,
		Balance:     FreeCreditsForNewUser,
		FreeCredits: FreeCreditsForNewUser,
		CreatedAt:   time.Now(),
		Transactions: []Transaction{{
			ID:        fmt.Sprintf("tx_%d", time.Now().UnixNano()),
			Type:      "free",
			Amount:    FreeCreditsForNewUser,
			Balance:   FreeCreditsForNewUser,
			Note:      "Welcome bonus",
			Timestamp: time.Now(),
		}},
	}

	m.users[userID] = user
	m.saveUser(user)
	return user
}

// GetBalance returns user's current balance
func (m *Manager) GetBalance(userID string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if user, exists := m.users[userID]; exists {
		return user.Balance
	}
	return 0
}

// UseCredits deducts credits for API usage
func (m *Manager) UseCredits(userID, model string, tokens int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	user, exists := m.users[userID]
	if !exists {
		return fmt.Errorf("user not found")
	}

	cost := getCost(model)
	if user.Balance < cost {
		return fmt.Errorf("insufficient credits: need %d, have %d", cost, user.Balance)
	}

	user.Balance -= cost
	user.TotalUsed += cost
	user.LastUsed = time.Now()

	user.Transactions = append(user.Transactions, Transaction{
		ID:        fmt.Sprintf("tx_%d", time.Now().UnixNano()),
		Type:      "use",
		Amount:    -cost,
		Balance:   user.Balance,
		Model:     model,
		Tokens:    tokens,
		Timestamp: time.Now(),
	})

	// Keep only last 100 transactions
	if len(user.Transactions) > 100 {
		user.Transactions = user.Transactions[len(user.Transactions)-100:]
	}

	return m.saveUser(user)
}

// AddCredits adds credits to user account
func (m *Manager) AddCredits(userID string, amount int, txType, note string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	user, exists := m.users[userID]
	if !exists {
		return fmt.Errorf("user not found")
	}

	user.Balance += amount
	if txType == "buy" {
		user.TotalBought += amount
	} else if txType == "free" {
		user.FreeCredits += amount
	}

	user.Transactions = append(user.Transactions, Transaction{
		ID:        fmt.Sprintf("tx_%d", time.Now().UnixNano()),
		Type:      txType,
		Amount:    amount,
		Balance:   user.Balance,
		Note:      note,
		Timestamp: time.Now(),
	})

	return m.saveUser(user)
}

// CheckCredits checks if user has enough credits
func (m *Manager) CheckCredits(userID, model string) (bool, int, int) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	user, exists := m.users[userID]
	if !exists {
		return false, 0, 0
	}

	cost := getCost(model)
	return user.Balance >= cost, user.Balance, cost
}

// GetUserInfo returns user credit info
func (m *Manager) GetUserInfo(userID string) *UserCredits {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if user, exists := m.users[userID]; exists {
		return user
	}
	return nil
}

func getCost(model string) int {
	if cost, ok := CreditCost[model]; ok {
		return cost
	}
	return 1 // Default cost
}

func (m *Manager) saveUser(user *UserCredits) error {
	path := filepath.Join(m.dataDir, user.UserID+".json")
	data, err := json.MarshalIndent(user, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func (m *Manager) loadAll() error {
	entries, err := os.ReadDir(m.dataDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		path := filepath.Join(m.dataDir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		var user UserCredits
		if err := json.Unmarshal(data, &user); err != nil {
			continue
		}

		m.users[user.UserID] = &user
	}

	return nil
}
