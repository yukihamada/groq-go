package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/viper"
)

// Config holds the application configuration
type Config struct {
	APIKey        string `mapstructure:"api_key"`
	Model         string `mapstructure:"model"`
	MoonshotKey   string `mapstructure:"moonshot_api_key"`
	OpenAIKey     string `mapstructure:"openai_api_key"`
}

// DefaultModel is the default LLM model
const DefaultModel = "llama-3.3-70b-versatile"

// Load loads configuration from environment and config files
func Load() (*Config, error) {
	v := viper.New()

	// Set defaults
	v.SetDefault("model", DefaultModel)

	// Config file paths
	home, err := os.UserHomeDir()
	if err == nil {
		configDir := filepath.Join(home, ".config", "groq-go")
		v.AddConfigPath(configDir)
	}
	v.AddConfigPath(".")
	v.SetConfigName("config")
	v.SetConfigType("yaml")

	// Environment variables
	v.SetEnvPrefix("GROQ")
	v.AutomaticEnv()

	// Bind specific env vars
	v.BindEnv("api_key", "GROQ_API_KEY")
	v.BindEnv("model", "GROQ_MODEL")
	v.BindEnv("moonshot_api_key", "MOONSHOT_API_KEY")
	v.BindEnv("openai_api_key", "OPENAI_API_KEY")

	// Read config file (optional)
	if err := v.ReadInConfig(); err != nil {
		// Config file is optional, so we only error on parse issues
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			// Only return error if it's not a "file not found" error
			if _, ok := err.(*os.PathError); !ok {
				return nil, fmt.Errorf("failed to read config: %w", err)
			}
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	// Validate required fields
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("GROQ_API_KEY environment variable is required")
	}

	return &cfg, nil
}
