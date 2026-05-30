package watch

import (
	"os"
	"path/filepath"
)

// snapshotFileName holds the last fully-accounted-for view of the notebook
// (normalized to LF). The watch loop diffs the live file against it to find
// what the engineer added since mimi last responded. It is persisted so a
// restart doesn't replay text that was already answered.
const snapshotFileName = ".mimi/watch.snapshot"

func snapshotPath(dir string) string {
	return filepath.Join(dir, snapshotFileName)
}

func snapshotExists(dir string) bool {
	_, err := os.Stat(snapshotPath(dir))
	return err == nil
}

// readSnapshot returns the stored snapshot, normalized to LF. Missing file → "".
func readSnapshot(dir string) string {
	data, _ := os.ReadFile(snapshotPath(dir))
	return normalizeLF(string(data))
}

// writeSnapshot stores content (caller passes already-normalized text).
func writeSnapshot(dir, content string) {
	path := snapshotPath(dir)
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, []byte(content), 0o644)
}

// clearSnapshot drops the anchor so the next Run re-anchors from scratch.
func clearSnapshot(dir string) {
	_ = os.Remove(snapshotPath(dir))
}
