package checkpoint

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func read(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return ""
	}
	return string(b)
}

func TestSnapshotUndoRestoresWorkTree(t *testing.T) {
	sessionDir := t.TempDir()
	work := t.TempDir()
	cp := New(sessionDir, work)
	if !cp.Enabled() {
		t.Skip("git not available")
	}

	write(t, work, "calc.go", "v1")
	if _, err := cp.Snapshot("session start"); err != nil {
		t.Fatalf("baseline: %v", err)
	}

	write(t, work, "calc.go", "v2")
	write(t, work, "new.go", "created in turn 1")
	if sha, err := cp.Snapshot("turn 1"); err != nil || sha == "" {
		t.Fatalf("turn 1 snapshot: sha=%q err=%v", sha, err)
	}

	// Undo one turn → back to baseline state.
	label, err := cp.Undo(1)
	if err != nil {
		t.Fatalf("undo: %v", err)
	}
	if label != "session start" {
		t.Fatalf("undo label = %q, want 'session start'", label)
	}
	if got := read(t, work, "calc.go"); got != "v1" {
		t.Fatalf("calc.go = %q, want v1 after undo", got)
	}
	if _, err := os.Stat(filepath.Join(work, "new.go")); !os.IsNotExist(err) {
		t.Fatalf("new.go should be removed after undo, stat err=%v", err)
	}
}

func TestSnapshotNoChangesIsNoop(t *testing.T) {
	cp := New(t.TempDir(), t.TempDir())
	if !cp.Enabled() {
		t.Skip("git not available")
	}
	write(t, cp.workTree, "a.txt", "x")
	if _, err := cp.Snapshot("first"); err != nil {
		t.Fatalf("first: %v", err)
	}
	sha, err := cp.Snapshot("no change")
	if err != nil {
		t.Fatalf("noop snapshot: %v", err)
	}
	if sha != "" {
		t.Fatalf("expected empty sha for no-op snapshot, got %q", sha)
	}
}

func TestUndoStopsAtBaseline(t *testing.T) {
	cp := New(t.TempDir(), t.TempDir())
	if !cp.Enabled() {
		t.Skip("git not available")
	}
	write(t, cp.workTree, "a.txt", "1")
	cp.Snapshot("session start")
	write(t, cp.workTree, "a.txt", "2")
	cp.Snapshot("turn 1")

	// Asking to undo more than exists clamps to baseline, not an error.
	if _, err := cp.Undo(99); err != nil {
		t.Fatalf("undo clamp: %v", err)
	}
	if got := read(t, cp.workTree, "a.txt"); got != "1" {
		t.Fatalf("a.txt = %q, want 1", got)
	}
	// Now at baseline; another undo should error.
	if _, err := cp.Undo(1); err == nil {
		t.Fatal("expected error undoing past baseline")
	}
}

func TestRealGitUntouched(t *testing.T) {
	cp := New(t.TempDir(), t.TempDir())
	if !cp.Enabled() {
		t.Skip("git not available")
	}
	// The shadow repo must live in the session dir, never in the work tree.
	if _, err := os.Stat(filepath.Join(cp.workTree, ".git")); !os.IsNotExist(err) {
		t.Fatalf("work tree should have no .git, stat err=%v", err)
	}
	write(t, cp.workTree, "a.txt", "1")
	cp.Snapshot("session start")
	if _, err := os.Stat(filepath.Join(cp.workTree, ".git")); !os.IsNotExist(err) {
		t.Fatalf("snapshot must not create .git in work tree, stat err=%v", err)
	}
	if _, err := os.Stat(cp.gitDir); err != nil {
		t.Fatalf("shadow gitDir should exist: %v", err)
	}
}
