package logger

import (
	"bufio"
	"encoding/json"
	"os"
	"testing"
)

func TestSessionLogging(t *testing.T) {
	// Use a temp dir so tests don't touch ~/.mimi
	dir := t.TempDir()
	LOG_DIR = dir

	s, err := StartSession("testid")
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	if err := Log("event", map[string]any{"msg": "first"}); err != nil {
		t.Fatalf("Log first: %v", err)
	}
	if err := Log("event", map[string]any{"msg": "second"}); err != nil {
		t.Fatalf("Log second: %v", err)
	}

	f, err := os.Open(s.Path)
	if err != nil {
		t.Fatalf("open log file: %v", err)
	}
	defer f.Close()

	var lines []map[string]any
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var entry map[string]any
		if err := json.Unmarshal(sc.Bytes(), &entry); err != nil {
			t.Fatalf("invalid JSON on line %d: %v", len(lines)+1, err)
		}
		lines = append(lines, entry)
	}
	if sc.Err() != nil {
		t.Fatalf("scanner: %v", sc.Err())
	}

	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}

	for i, entry := range lines {
		if entry["session"] != "testid" {
			t.Errorf("line %d: session = %q, want testid", i+1, entry["session"])
		}
		if entry["kind"] != "event" {
			t.Errorf("line %d: kind = %q, want event", i+1, entry["kind"])
		}
		if _, ok := entry["t"]; !ok {
			t.Errorf("line %d: missing field t", i+1)
		}
		if _, ok := entry["data"]; !ok {
			t.Errorf("line %d: missing field data", i+1)
		}
	}
}
