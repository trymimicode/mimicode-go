package main

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/trymimicode/mimicode-go/internal/config"
)

// mimicodeConfig is kept as an alias so existing call-sites compile unchanged.
type mimicodeConfig = config.Config

func configFilePath() (string, error) { return config.FilePath() }
func loadConfig() (config.Config, error) { return config.Load() }
func saveConfig(cfg config.Config) error { return config.Save(cfg) }

// applyStoredKey sets all provider env vars from the config file.
func applyStoredKey() { config.ApplyAll() }

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
		// Strip an env-var prefix like "ANTHROPIC_API_KEY=sk-ant-..." if present.
		if i := strings.IndexByte(setKey, '='); i >= 0 {
			setKey = setKey[i+1:]
		}
		cfg.AnthropicAPIKey = strings.TrimSpace(setKey)
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
