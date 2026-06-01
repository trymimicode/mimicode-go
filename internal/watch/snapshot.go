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

// readSnapshot returns the stored snapshot, normalized to LF. Missing file → ("", nil).
func readSnapshot(dir string) (string, error) {
	data, err := os.ReadFile(snapshotPath(dir))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return normalizeLF(string(data)), nil
}

// writeSnapshot stores content (caller passes already-normalized text).
func writeSnapshot(dir, content string) error {
	path := snapshotPath(dir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// clearSnapshot drops the anchor so the next Run re-anchors from scratch.
func clearSnapshot(dir string) error {
	err := os.Remove(snapshotPath(dir))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
