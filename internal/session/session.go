package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/trymimicode/mimicode-go/internal/logger"
	"github.com/trymimicode/mimicode-go/internal/provider"
)

type Session struct {
	ID   string
	Path string // ~/.mimi/sessions/<id>.jsonl
}

func MessagesPath(session Session) string {
	if strings.HasSuffix(session.Path, ".jsonl") {
		return strings.TrimSuffix(session.Path, ".jsonl") + ".messages.json"
	}
	return session.Path + ".messages.json"
}

func LoadMessages(session Session) ([]provider.Message, error) {
	path := MessagesPath(session)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return []provider.Message{}, nil
	}
	if err != nil {
		return nil, err
	}

	var messages []provider.Message
	if err := json.Unmarshal(data, &messages); err != nil {
		fmt.Fprintf(os.Stderr, "session: warning: decode %s: %v\n", path, err)
		return []provider.Message{}, nil
	}
	return messages, nil
}

func SaveMessages(session Session, messages []provider.Message) error {
	path := MessagesPath(session)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(messages, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func MessagesCount(session Session) int {
	messages, err := LoadMessages(session)
	if err != nil {
		return 0
	}
	return len(messages)
}

func ResumeOrNew(sessionID string) Session {
	s, err := logger.StartSession(sessionID)
	if err != nil {
		return Session{}
	}
	current := logger.CurrentSession()
	if current == nil {
		current = s
	}
	return Session{ID: current.ID, Path: current.Path}
}
