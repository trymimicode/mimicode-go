package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandleMemoryWrite_TwoSections(t *testing.T) {
	dir := t.TempDir()

	// ── First write ───────────────────────────────────────────────────────────
	r1 := HandleMemoryWrite("sess1", map[string]any{
		"component":     "auth",
		"summary":       "Added JWT middleware",
		"detail":        "Uses HS256 with 24h expiry.",
		"related_files": []string{"internal/auth/jwt.go", "cmd/server/main.go"},
		"tags":          []string{"auth", "security"},
		"change_entry":  map[string]any{"file": "jwt.go", "what": "add Validate()", "why": "token verification"},
		"open_issues":   []string{"rotate signing key", "add refresh tokens"},
	}, dir)

	if r1 != "memory written: auth" {
		t.Fatalf("first write: got %q", r1)
	}

	// ── Second write ──────────────────────────────────────────────────────────
	r2 := HandleMemoryWrite("sess1", map[string]any{
		"component": "database",
		"summary":   "Migrated to pgx/v5",
	}, dir)

	if r2 != "memory written: database" {
		t.Fatalf("second write: got %q", r2)
	}

	// ── Verify file contents ──────────────────────────────────────────────────
	path := filepath.Join(dir, ".mimi", "MEMORY.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read MEMORY.md: %v", err)
	}
	content := string(raw)

	// Both section headers must appear.
	if !strings.Contains(content, "## auth —") {
		t.Error("missing '## auth —' section header")
	}
	if !strings.Contains(content, "## database —") {
		t.Error("missing '## database —' section header")
	}

	// First section fields.
	for _, want := range []string{
		"**Summary:** Added JWT middleware",
		"Uses HS256 with 24h expiry.",
		"**Files:** internal/auth/jwt.go, cmd/server/main.go",
		"**Tags:** auth, security",
		"**Change:** jwt.go: add Validate() (token verification)",
		"**Open issues:**",
		"- rotate signing key",
		"- add refresh tokens",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("missing %q", want)
		}
	}

	// Second section summary.
	if !strings.Contains(content, "**Summary:** Migrated to pgx/v5") {
		t.Error("missing second section summary")
	}

	// Sections appear in write order.
	authIdx := strings.Index(content, "## auth")
	dbIdx := strings.Index(content, "## database")
	if authIdx >= dbIdx {
		t.Error("auth section should precede database section")
	}
}

func TestHandleMemoryWrite_MissingRequired(t *testing.T) {
	dir := t.TempDir()

	if r := HandleMemoryWrite("s", map[string]any{"summary": "x"}, dir); !strings.Contains(r, "component") {
		t.Errorf("missing component: got %q", r)
	}
	if r := HandleMemoryWrite("s", map[string]any{"component": "x"}, dir); !strings.Contains(r, "summary") {
		t.Errorf("missing summary: got %q", r)
	}
}

func TestLoadMemory_Missing(t *testing.T) {
	if got := LoadMemory(t.TempDir()); got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestLoadRules_Missing(t *testing.T) {
	if got := LoadRules(t.TempDir()); got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestLoadMemory_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	HandleMemoryWrite("s", map[string]any{"component": "cache", "summary": "LRU added"}, dir)
	got := LoadMemory(dir)
	if !strings.Contains(got, "LRU added") {
		t.Errorf("LoadMemory did not return written content: %q", got)
	}
}

func TestLoadRules_ReadsFile(t *testing.T) {
	dir := t.TempDir()
	mimiPath := filepath.Join(dir, ".mimi")
	os.MkdirAll(mimiPath, 0o755)
	os.WriteFile(filepath.Join(mimiPath, "RULES.md"), []byte("always use table-driven tests"), 0o644)

	got := LoadRules(dir)
	if !strings.Contains(got, "table-driven tests") {
		t.Errorf("LoadRules did not return file content: %q", got)
	}
}
