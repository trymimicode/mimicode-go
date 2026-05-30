package watch

import (
	"encoding/json"
	"os"
	"path/filepath"
)

const lockFileName = ".mimi/watch.lock"

// watchState is persisted across process restarts so we never replay old content.
type watchState struct {
	SessionID       string `json:"session_id"`
	ProcessedOffset int64  `json:"processed_offset"` // byte offset: everything before here is done
	Processing      bool   `json:"processing"`        // true while agent is running
}

func stateLockPath(dir string) string {
	return filepath.Join(dir, lockFileName)
}

func readState(dir string) watchState {
	data, err := os.ReadFile(stateLockPath(dir))
	if err != nil {
		return watchState{}
	}
	var s watchState
	_ = json.Unmarshal(data, &s)
	return s
}

func writeState(dir string, s watchState) {
	path := stateLockPath(dir)
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	data, _ := json.MarshalIndent(s, "", "  ")
	_ = os.WriteFile(path, data, 0o644)
}
