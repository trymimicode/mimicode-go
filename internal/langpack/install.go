package langpack

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const remoteBase = "https://raw.githubusercontent.com/trymimicode/language-packs/main/languages"

// PackEntry is a single record in packs.lock.
type PackEntry struct {
	Language    string `json:"language"`
	Source      string `json:"source"`
	InstalledAt string `json:"installed_at"`
}

// PacksLock is the on-disk structure of .mimi/packs.lock.
type PacksLock struct {
	Packs []PackEntry `json:"packs"`
}

// Install downloads the language pack for the given language from the remote
// registry and writes it into .mimi/ of the project at cwd.
//
//   - AGENTS.md  → .mimi/AGENTS.md  (pack content first; user's root AGENTS.md
//                  appended after so it takes LLM priority over the pack)
//   - RULES.md   → appended to .mimi/RULES.md
//   - packs.lock → updated with this install record
func Install(cwd, language string) error {
	language = strings.ToLower(strings.TrimSpace(language))
	if language == "" {
		return fmt.Errorf("language is required")
	}

	mimiDir := filepath.Join(cwd, ".mimi")
	if err := os.MkdirAll(mimiDir, 0o755); err != nil {
		return fmt.Errorf("create .mimi: %w", err)
	}

	agentsContent, err := fetchFile(language, "AGENTS.md")
	if err != nil {
		return err
	}
	rulesContent, err := fetchFile(language, "RULES.md")
	if err != nil {
		return err
	}

	if err := mergeAgents(cwd, mimiDir, language, agentsContent); err != nil {
		return fmt.Errorf("write .mimi/AGENTS.md: %w", err)
	}
	if err := appendRules(mimiDir, language, rulesContent); err != nil {
		return fmt.Errorf("write .mimi/RULES.md: %w", err)
	}
	if err := updateLock(mimiDir, language); err != nil {
		return fmt.Errorf("write .mimi/packs.lock: %w", err)
	}
	return nil
}

func fetchFile(language, filename string) (string, error) {
	url := fmt.Sprintf("%s/%s/%s", remoteBase, language, filename)
	resp, err := http.Get(url) //nolint:gosec
	if err != nil {
		return "", fmt.Errorf("fetch %s/%s: %w", language, filename, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("language pack %q not found — check https://github.com/trymimicode/language-packs", language)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status %d fetching %s/%s", resp.StatusCode, language, filename)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read %s/%s: %w", language, filename, err)
	}
	return strings.TrimSpace(string(data)), nil
}

// mergeAgents writes .mimi/AGENTS.md.
// Layout: pack content first, then the user's root AGENTS.md (if present).
// LLMs weight later content more heavily, so user instructions win on clashes.
func mergeAgents(cwd, mimiDir, language, packContent string) error {
	var b strings.Builder
	fmt.Fprintf(&b, "# Language Pack: %s\n\n", language)
	b.WriteString(packContent)
	b.WriteString("\n")

	rootAgents := filepath.Join(cwd, "AGENTS.md")
	if data, err := os.ReadFile(rootAgents); err == nil {
		userContent := strings.TrimSpace(string(data))
		if userContent != "" {
			b.WriteString("\n# Project Instructions\n")
			b.WriteString("<!-- Appended from AGENTS.md — these instructions take priority over the language pack above on any clash. -->\n\n")
			b.WriteString(userContent)
			b.WriteString("\n")
		}
	}

	return os.WriteFile(filepath.Join(mimiDir, "AGENTS.md"), []byte(b.String()), 0o644)
}

// appendRules appends the pack's RULES.md content to .mimi/RULES.md under a
// named section so it is clearly attributed and easy to remove later.
func appendRules(mimiDir, language, rulesContent string) error {
	if strings.TrimSpace(rulesContent) == "" {
		return nil
	}
	path := filepath.Join(mimiDir, "RULES.md")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "\n## Language Pack: %s\n%s\n", language, rulesContent)
	return err
}

// updateLock writes or updates .mimi/packs.lock with the newly installed pack.
// Re-installing the same language replaces its existing entry.
func updateLock(mimiDir, language string) error {
	lockPath := filepath.Join(mimiDir, "packs.lock")

	var lock PacksLock
	if data, err := os.ReadFile(lockPath); err == nil {
		_ = json.Unmarshal(data, &lock)
	}

	// Replace existing entry for this language.
	out := lock.Packs[:0]
	for _, p := range lock.Packs {
		if p.Language != language {
			out = append(out, p)
		}
	}
	lock.Packs = append(out, PackEntry{
		Language:    language,
		Source:      "trymimicode/language-packs",
		InstalledAt: time.Now().UTC().Format(time.RFC3339),
	})

	data, err := json.MarshalIndent(lock, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(lockPath, data, 0o644)
}
