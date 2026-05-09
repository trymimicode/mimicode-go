package logger

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var LOG_DIR string

func init() {
	home, err := os.UserHomeDir()
	if err != nil {
		panic(fmt.Sprintf("logger: cannot determine home directory: %v", err))
	}
	LOG_DIR = filepath.Join(home, ".mimi", "sessions")
}

type Session struct {
	ID    string
	Start time.Time
	Path  string
	mu    sync.Mutex
}

var current *Session

func NewSession(id string) (*Session, error) {
	if id == "" {
		b := make([]byte, 6)
		if _, err := rand.Read(b); err != nil {
			return nil, fmt.Errorf("logger: generate id: %w", err)
		}
		id = hex.EncodeToString(b)
	}

	if err := os.MkdirAll(LOG_DIR, 0o755); err != nil {
		return nil, fmt.Errorf("logger: create log dir: %w", err)
	}

	return &Session{
		ID:    id,
		Start: time.Now(),
		Path:  filepath.Join(LOG_DIR, id+".jsonl"),
	}, nil
}

func StartSession(id string) (*Session, error) {
	s, err := NewSession(id)
	if err != nil {
		return nil, err
	}
	current = s
	return s, nil
}

func Log(kind string, data map[string]any) error {
	if current == nil {
		return fmt.Errorf("logger: no active session")
	}
	return current.Log(kind, data)
}

func (s *Session) Log(kind string, data map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry := map[string]any{
		"t":       time.Since(s.Start).Seconds(),
		"session": s.ID,
		"kind":    kind,
		"data":    data,
	}

	line, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("logger: marshal entry: %w", err)
	}

	f, err := os.OpenFile(s.Path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("logger: open log file: %w", err)
	}
	defer f.Close()

	_, err = fmt.Fprintf(f, "%s\n", line)
	return err
}

func CurrentSession() *Session {
	return current
}
