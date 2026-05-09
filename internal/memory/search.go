package memory

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	_ "modernc.org/sqlite"
)

type SearchResult struct {
	Kind     string // "session" | "memory" | "rules"
	SourceID string
	Snippet  string
	Rank     float64
}

func connect(dbPath string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}

	_, err = db.Exec(`CREATE VIRTUAL TABLE IF NOT EXISTS memory USING fts5(
kind UNINDEXED, source_id UNINDEXED, text, file_scope,
tokenize='unicode61 remove_diacritics 2'
)`)
	if err != nil {
		db.Close()
		return nil, err
	}

	return db, nil
}

func reindex(db *sql.DB, sessionsDir, memoryRoot string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.Exec("DELETE FROM memory"); err != nil {
		return err
	}

	sessionPaths, err := filepath.Glob(filepath.Join(sessionsDir, "*.messages.json"))
	if err != nil {
		return err
	}
	for _, path := range sessionPaths {
		text, scope, err := sessionSearchText(path)
		if err != nil {
			return err
		}
		sourceID := strings.TrimSuffix(filepath.Base(path), ".messages.json")
		if _, err := tx.Exec(
			"INSERT INTO memory(kind, source_id, text, file_scope) VALUES(?, ?, ?, ?)",
			"session", sourceID, text, scope,
		); err != nil {
			return err
		}
	}

	for _, item := range []struct {
		name string
		kind string
	}{
		{name: "MEMORY.md", kind: "memory"},
		{name: "RULES.md", kind: "rules"},
	} {
		data, err := os.ReadFile(filepath.Join(memoryRoot, item.name))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(
			"INSERT INTO memory(kind, source_id, text, file_scope) VALUES(?, ?, ?, ?)",
			item.kind, item.name, string(data), "",
		); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func escapeQuery(q string) string {
	if strings.ContainsAny(q, `"()`) || containsFTSKeyword(q) {
		return q
	}

	parts := strings.Fields(q)
	for i, part := range parts {
		parts[i] = `"` + part + `"`
	}
	return strings.Join(parts, " ")
}

func Search(query string, topK int, kind string, cwd string) ([]SearchResult, error) {
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return nil, err
		}
	}
	if topK <= 0 {
		topK = 10
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	db, err := connect(filepath.Join(cwd, ".mimi", "sessions.db"))
	if err != nil {
		return nil, err
	}
	defer db.Close()

	if err := reindex(db, filepath.Join(home, ".mimi", "sessions"), filepath.Join(cwd, ".mimi")); err != nil {
		return nil, err
	}

	match := escapeQuery(query)
	sqlQuery := "SELECT kind, source_id, snippet(memory, 2, '<<', '>>', '…', 16), rank FROM memory WHERE memory MATCH ?"
	args := []any{match}
	if kind != "" {
		sqlQuery += " AND kind = ?"
		args = append(args, kind)
	}
	sqlQuery += " ORDER BY rank LIMIT ?"
	args = append(args, topK)

	rows, err := db.Query(sqlQuery, args...)
	if err != nil {
		return []SearchResult{}, nil
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var result SearchResult
		if err := rows.Scan(&result.Kind, &result.SourceID, &result.Snippet, &result.Rank); err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	if err := rows.Err(); err != nil {
		return []SearchResult{}, nil
	}

	return results, nil
}

func FormatResults(results []SearchResult, query string) string {
	if len(results) == 0 {
		return "[memory_search] no matches for: " + query
	}

	var b strings.Builder
	fmt.Fprintf(&b, "[memory_search] %d match(es) for: %s", len(results), query)
	for _, result := range results {
		fmt.Fprintf(&b, "\n--- %s: %s ---\n%s", result.Kind, result.SourceID, result.Snippet)
	}
	return b.String()
}

type transcriptMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type transcriptBlock struct {
	Type  string         `json:"type"`
	Text  string         `json:"text"`
	Name  string         `json:"name"`
	Input map[string]any `json:"input"`
}

func sessionSearchText(path string) (string, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", err
	}

	var messages []transcriptMessage
	if err := json.Unmarshal(data, &messages); err != nil {
		return "", "", err
	}

	var text strings.Builder
	paths := make(map[string]struct{})
	for _, msg := range messages {
		var plain string
		if err := json.Unmarshal(msg.Content, &plain); err == nil {
			writeSearchLine(&text, msg.Role, plain)
			continue
		}

		var blocks []transcriptBlock
		if err := json.Unmarshal(msg.Content, &blocks); err != nil {
			continue
		}
		for _, block := range blocks {
			switch block.Type {
			case "text":
				writeSearchLine(&text, msg.Role, block.Text)
			case "tool_use":
				if path, ok := block.Input["path"].(string); ok && path != "" {
					fmt.Fprintf(&text, "[tool:%s] %s\n", block.Name, path)
					paths[path] = struct{}{}
				}
			}
		}
	}

	return text.String(), joinSet(paths), nil
}

func writeSearchLine(b *strings.Builder, role, content string) {
	if content == "" {
		return
	}
	fmt.Fprintf(b, "[%s] %s\n", role, content)
}

func containsFTSKeyword(q string) bool {
	for _, part := range strings.Fields(q) {
		switch strings.ToUpper(part) {
		case "AND", "OR", "NOT", "NEAR":
			return true
		}
	}
	return false
}

func joinSet(values map[string]struct{}) string {
	if len(values) == 0 {
		return ""
	}

	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return strings.Join(out, " ")
}
