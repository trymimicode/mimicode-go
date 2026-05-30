package watch

import (
	"os"
	"strings"
)

// trackingHeader is stamped into a brand-new notebook so the engineer can see,
// the moment the file is created, that mimi is watching it.
const trackingHeader = "● mimicode is tracking this file\n" +
	"  write below, save, and the answer appears under your text.\n" +
	"  append, edit in place, or clear and start over — all work.\n" +
	"\n"

// ensureNotebook creates path as an empty file if it does not exist. Existing
// content is never touched.
func ensureNotebook(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	return os.WriteFile(path, []byte(trackingHeader), 0o644)
}

// stampHeaderIfEmpty writes the tracking header into a notebook that is empty
// (size 0). It is a no-op on any file that already has content, so the
// engineer's own text is never disturbed. Returns true if the header was
// written.
func stampHeaderIfEmpty(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.Size() > 0 {
		return false
	}
	if err := os.WriteFile(path, []byte(trackingHeader), 0o644); err != nil {
		return false
	}
	return true
}

// appendResponse appends mimi's answer below whatever is currently in the file,
// separated by a blank line. It returns the exact text appended so the caller
// can extend the snapshot in memory — that way any edits the engineer made
// while mimi was thinking stay outside the snapshot and get answered next tick,
// instead of being silently swallowed by a re-read.
func appendResponse(path, response string) (string, error) {
	suffix := "\n" + strings.TrimSpace(response) + "\n"
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.WriteString(suffix); err != nil {
		return "", err
	}
	return suffix, nil
}
