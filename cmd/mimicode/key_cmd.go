package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type mimicodeConfig struct {
	AnthropicAPIKey string `json:"anthropic_api_key,omitempty"`
}

func configFilePath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "mimicode", "config.json"), nil
}

func loadConfig() (mimicodeConfig, error) {
	path, err := configFilePath()
	if err != nil {
		return mimicodeConfig{}, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return mimicodeConfig{}, nil
	}
	if err != nil {
		return mimicodeConfig{}, err
	}
	var cfg mimicodeConfig
	return cfg, json.Unmarshal(data, &cfg)
}

func saveConfig(cfg mimicodeConfig) error {
	path, err := configFilePath()
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

// applyStoredKey sets ANTHROPIC_API_KEY from the config file if the env var is not already set.
func applyStoredKey() {
	if getenv("ANTHROPIC_API_KEY") != "" {
		return
	}
	cfg, err := loadConfig()
	if err != nil || cfg.AnthropicAPIKey == "" {
		return
	}
	_ = setenv("ANTHROPIC_API_KEY", cfg.AnthropicAPIKey)
}

func runKeyCmd(args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("mimicode key", flag.ContinueOnError)
	fs.SetOutput(errOut)

	var setKey string
	var viewKey bool
	fs.StringVar(&setKey, "set", "", "save the Anthropic API key globally")
	fs.BoolVar(&viewKey, "view", false, "print the currently saved API key")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	if setKey != "" {
		cfg, err := loadConfig()
		if err != nil {
			fmt.Fprintf(errOut, "mimicode: load config: %v\n", err)
			return 1
		}
		cfg.AnthropicAPIKey = setKey
		if err := saveConfig(cfg); err != nil {
			fmt.Fprintf(errOut, "mimicode: save config: %v\n", err)
			return 1
		}
		path, _ := configFilePath()
		fmt.Fprintf(out, "API key saved to %s\n", path)
		return 0
	}

	if viewKey {
		cfg, err := loadConfig()
		if err != nil {
			fmt.Fprintf(errOut, "mimicode: load config: %v\n", err)
			return 1
		}
		if cfg.AnthropicAPIKey == "" {
			fmt.Fprintln(out, "(no key saved)")
		} else {
			fmt.Fprintln(out, cfg.AnthropicAPIKey)
		}
		return 0
	}

	fmt.Fprintln(errOut, "usage: mimicode key --set <key>\n       mimicode key --view")
	return 2
}
