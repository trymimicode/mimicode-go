package watch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureNotebookCreatesOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "code.mimi")
	if err := ensureNotebook(path); err != nil {
		t.Fatalf("ensureNotebook: %v", err)
	}
	if err := os.WriteFile(path, []byte("my own thoughts\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Second call must not clobber existing content.
	if err := ensureNotebook(path); err != nil {
		t.Fatalf("ensureNotebook (2): %v", err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "my own thoughts\n" {
		t.Errorf("ensureNotebook overwrote existing notebook: %q", string(data))
	}
}

func TestReadThinkingStripsBrief(t *testing.T) {
	path := filepath.Join(t.TempDir(), "code.mimi")
	content := "i think pgx pools are not goroutine safe\n" + BriefMarker + "\nactually the pool is safe\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	got, ok := readThinking(path)
	if !ok {
		t.Fatal("readThinking returned !ok")
	}
	if strings.Contains(got, BriefMarker) || strings.Contains(got, "actually the pool is safe") {
		t.Errorf("readThinking did not strip the injected brief: %q", got)
	}
	if !strings.Contains(got, "goroutine safe") {
		t.Errorf("readThinking dropped the engineer's text: %q", got)
	}
}

func TestAppendBriefIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "code.mimi")
	if err := os.WriteFile(path, []byte("engineer thinking\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := appendBrief(path, "first brief"); err != nil {
		t.Fatal(err)
	}
	if err := appendBrief(path, "second brief"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	got := string(data)
	// Engineer's text survives; only one marker; latest brief present, old gone.
	if strings.Count(got, BriefMarker) != 1 {
		t.Errorf("expected exactly one brief marker, got %d:\n%s", strings.Count(got, BriefMarker), got)
	}
	if !strings.Contains(got, "engineer thinking") {
		t.Errorf("engineer text lost:\n%s", got)
	}
	if strings.Contains(got, "first brief") || !strings.Contains(got, "second brief") {
		t.Errorf("brief not replaced cleanly:\n%s", got)
	}
}
