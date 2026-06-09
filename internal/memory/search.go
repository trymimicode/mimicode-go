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

	// idx_state tracks the mtime of each indexed source so reindex can touch only
	// what changed instead of rebuilding the whole corpus on every search.
	if _, err = db.Exec(`CREATE TABLE IF NOT EXISTS idx_state(
key TEXT PRIMARY KEY, mtime INTEGER
)`); err != nil {
		db.Close()
		return nil, err
	}

	return db, nil
}

// indexSource is one indexable item (a session transcript or a memory file).
type indexSource struct {
	key      string // unique: "<kind>/<sourceID>"
	kind     string
	sourceID string
	mtime    int64
	load     func() (text, scope string, err error)
}

// gatherSources lists every source that should be in the index, with its mtime
// and a lazy loader so unchanged sources are never re-read.
func gatherSources(sessionsDir, memoryRoot string) ([]indexSource, error) {
	var sources []indexSource

	sessionPaths, err := filepath.Glob(filepath.Join(sessionsDir, "*.messages.json"))
	if err != nil {
		return nil, err
	}
	for _, path := range sessionPaths {
		fi, err := os.Stat(path)
		if err != nil {
			continue
		}
		sourceID := strings.TrimSuffix(filepath.Base(path), ".messages.json")
		path := path
		sources = append(sources, indexSource{
			key:      "session/" + sourceID,
			kind:     "session",
			sourceID: sourceID,
			mtime:    fi.ModTime().UnixNano(),
			load:     func() (string, string, error) { return sessionSearchText(path) },
		})
	}

	for _, item := range []struct{ name, kind string }{
		{"MEMORY.md", "memory"},
		{"RULES.md", "rules"},
	} {
		path := filepath.Join(memoryRoot, item.name)
		fi, err := os.Stat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		item, path := item, path
		sources = append(sources, indexSource{
			key:      item.kind + "/" + item.name,
			kind:     item.kind,
			sourceID: item.name,
			mtime:    fi.ModTime().UnixNano(),
			load: func() (string, string, error) {
				data, err := os.ReadFile(path)
				return string(data), "", err
			},
		})
	}

	return sources, nil
}

// reindex brings the FTS table up to date incrementally: it re-reads only
// sources whose mtime changed (or are new) and drops sources whose files are
// gone. Within a session, searching repeatedly no longer re-parses every
// transcript on disk.
func reindex(db *sql.DB, sessionsDir, memoryRoot string) error {
	sources, err := gatherSources(sessionsDir, memoryRoot)
	if err != nil {
		return err
	}

	state := map[string]int64{}
	rows, err := db.Query("SELECT key, mtime FROM idx_state")
	if err != nil {
		return err
	}
	for rows.Next() {
		var key string
		var mtime int64
		if err := rows.Scan(&key, &mtime); err != nil {
			rows.Close()
			return err
		}
		state[key] = mtime
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	desired := make(map[string]bool, len(sources))
	for _, s := range sources {
		desired[s.key] = true
		if old, ok := state[s.key]; ok && old == s.mtime {
			continue // unchanged since last index
		}
		text, scope, err := s.load()
		if err != nil {
			return err
		}
		if _, err := tx.Exec("DELETE FROM memory WHERE kind = ? AND source_id = ?", s.kind, s.sourceID); err != nil {
			return err
		}
		if _, err := tx.Exec(
			"INSERT INTO memory(kind, source_id, text, file_scope) VALUES(?, ?, ?, ?)",
			s.kind, s.sourceID, text, scope,
		); err != nil {
			return err
		}
		if _, err := tx.Exec(
			"INSERT INTO idx_state(key, mtime) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET mtime = excluded.mtime",
			s.key, s.mtime,
		); err != nil {
			return err
		}
	}

	// Drop sources whose files no longer exist.
	for key := range state {
		if desired[key] {
			continue
		}
		kind, sourceID, _ := strings.Cut(key, "/")
		if _, err := tx.Exec("DELETE FROM memory WHERE kind = ? AND source_id = ?", kind, sourceID); err != nil {
			return err
		}
		if _, err := tx.Exec("DELETE FROM idx_state WHERE key = ?", key); err != nil {
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
