package memory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSearchIndexesSessionsAndMemory(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	sessionsDir := filepath.Join(home, ".mimi", "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	memoryDir := filepath.Join(cwd, ".mimi")
	if err := os.MkdirAll(memoryDir, 0o755); err != nil {
		t.Fatalf("mkdir memory: %v", err)
	}

	writeSession(t, filepath.Join(sessionsDir, "first.messages.json"), []map[string]any{
		{
			"role": "user",
			"content": []map[string]any{
				{"type": "text", "text": "Please investigate the frobnicator pipeline."},
			},
		},
		{
			"role": "assistant",
			"content": []map[string]any{
				{"type": "tool_use", "name": "read_file", "input": map[string]any{"path": "internal/frob.go"}},
			},
		},
	})
	writeSession(t, filepath.Join(sessionsDir, "second.messages.json"), []map[string]any{
		{
			"role": "user",
			"content": []map[string]any{
				{"type": "text", "text": "Discuss unrelated routing behavior."},
			},
		},
	})
	if err := os.WriteFile(filepath.Join(memoryDir, "MEMORY.md"), []byte("Cache notes mention durable embeddings."), 0o644); err != nil {
		t.Fatalf("write memory: %v", err)
	}

	results, err := Search("frobnicator", 5, "", cwd)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one search result")
	}

	var found bool
	for _, result := range results {
		if result.Kind == "session" && result.SourceID == "first" && strings.Contains(result.Snippet, "frobnicator") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected first session result for frobnicator, got %#v", results)
	}

	results, err = Search("embeddings", 5, "memory", cwd)
	if err != nil {
		t.Fatalf("Search memory: %v", err)
	}
	if len(results) == 0 || results[0].Kind != "memory" || results[0].SourceID != "MEMORY.md" {
		t.Fatalf("expected MEMORY.md result for embeddings, got %#v", results)
	}
}

func writeSession(t *testing.T, path string, messages []map[string]any) {
	t.Helper()

	data, err := json.Marshal(messages)
	if err != nil {
		t.Fatalf("marshal session: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}
}
