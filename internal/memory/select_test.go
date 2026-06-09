package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeMemory(t *testing.T, cwd, content string) {
	t.Helper()
	dir := filepath.Join(cwd, ".mimi")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "MEMORY.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write memory: %v", err)
	}
}

func TestSelectMemoryReturnsWholeFileUnderBudget(t *testing.T) {
	cwd := t.TempDir()
	content := "## entry one\nsmall\n\n## entry two\nalso small\n"
	writeMemory(t, cwd, content)

	if got := SelectMemory(cwd, "anything", 10_000); got != content {
		t.Errorf("small file should be returned whole:\n got %q\nwant %q", got, content)
	}
}

func TestSelectMemoryPrefersRelevantEntries(t *testing.T) {
	cwd := t.TempDir()
	// Several entries; only one mentions the rate limiter. Budget fits a couple,
	// not all, so the relevant entry must be chosen and others dropped.
	pad := strings.Repeat("filler ", 20)
	content := "## auth flow\n" + pad + "\n\n" +
		"## rate limiter\nthe token bucket throttles requests " + pad + "\n\n" +
		"## ui theme\n" + pad + "\n\n" +
		"## logging\n" + pad + "\n\n" +
		"## caching\n" + pad + "\n"
	writeMemory(t, cwd, content)

	got := SelectMemory(cwd, "how does the rate limiter throttle", 400)
	if !strings.Contains(got, "rate limiter") {
		t.Errorf("expected the relevant entry to be selected, got:\n%s", got)
	}
	if len(got) >= len(content) {
		t.Errorf("selection should drop entries, got %d of %d bytes", len(got), len(content))
	}
	if !strings.Contains(got, "omitted") {
		t.Error("expected a truncation marker when entries were dropped")
	}
}

func TestSelectMemoryFallsBackToRecentWhenNoMatch(t *testing.T) {
	cwd := t.TempDir()
	pad := strings.Repeat("x ", 200)
	content := "## old\n" + pad + "\n\n## newest\nNEEDLE " + pad + "\n"
	writeMemory(t, cwd, content)

	// Query matches nothing; the most recent entry should win the budget.
	got := SelectMemory(cwd, "zzzzz nonexistent terms", 300)
	if !strings.Contains(got, "NEEDLE") {
		t.Errorf("no-match query should fall back to the most recent entry, got:\n%s", got)
	}
}
