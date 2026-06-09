// Package config manages mimicode's persistent configuration file.
package config

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Config is the on-disk configuration structure.
type Config struct {
	AnthropicAPIKey string `json:"anthropic_api_key,omitempty"`
	MoonshotAPIKey  string `json:"moonshot_api_key,omitempty"`
	MinimaxAPIKey   string `json:"minimax_api_key,omitempty"`
}

// FilePath returns the platform config file path.
func FilePath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "mimicode", "config.json"), nil
}

// Load reads the config file. Returns a zero Config if the file does not exist.
func Load() (Config, error) {
	path, err := FilePath()
	if err != nil {
		return Config{}, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, err
	}
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")) // strip UTF-8 BOM
	var cfg Config
	return cfg, json.Unmarshal(data, &cfg)
}

// Save writes cfg to disk, creating parent directories as needed.
func Save(cfg Config) error {
	path, err := FilePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

// ApplyAll sets provider env vars from the config file for any that are not
// already present in the environment. Safe to call multiple times.
func ApplyAll() {
	cfg, err := Load()
	if err != nil {
		return
	}
	apply := func(envVar, stored string) {
		if os.Getenv(envVar) == "" && strings.TrimSpace(stored) != "" {
			_ = os.Setenv(envVar, strings.TrimSpace(stored))
		}
	}
	apply("ANTHROPIC_API_KEY", cfg.AnthropicAPIKey)
	apply("MOONSHOT_API_KEY", cfg.MoonshotAPIKey)
	apply("MINIMAX_API_KEY", cfg.MinimaxAPIKey)
}

// SaveKey persists a single provider API key and also sets the env var in the
// current process so subsequent calls pick it up immediately.
func SaveKey(envVar, key string) error {
	key = strings.TrimSpace(key)
	cfg, err := Load()
	if err != nil {
		cfg = Config{}
	}
	switch envVar {
	case "ANTHROPIC_API_KEY":
		cfg.AnthropicAPIKey = key
	case "MOONSHOT_API_KEY":
		cfg.MoonshotAPIKey = key
	case "MINIMAX_API_KEY":
		cfg.MinimaxAPIKey = key
	}
	if err := Save(cfg); err != nil {
		return err
	}
	return os.Setenv(envVar, key)
}
