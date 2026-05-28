package store

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/trymimicode/mimicode-go/internal/provider"
)

var sessionsDir string

func init() {
	home, err := os.UserHomeDir()
	if err != nil {
		panic("store: cannot determine home directory: " + err.Error())
	}
	sessionsDir = filepath.Join(home, ".mimi", "sessions")
}

// Session owns all persistence for one conversation: event log + messages.
type Session struct {
	ID        string    `json:"id"`
	CWD       string    `json:"cwd"`
	StartedAt time.Time `json:"started_at"`
	Model     string    `json:"model"`

	mu      sync.Mutex
	f       *os.File // events.jsonl kept open — no open/close per write
	start   time.Time
	dir     string // ~/.mimi/sessions/<id>/
	turnNum int    // incremented by LogUser
}

// ── Event types ───────────────────────────────────────────────────────────────

// CallRec is one tool_use block from a model response.
type CallRec struct {
	ID    string         `json:"id"`
	Name  string         `json:"name"`
	Input map[string]any `json:"input"`
}

// TokenRec is the token cost for one model API call.
type TokenRec struct {
	In int `json:"in"`
	Out int `json:"out"`
	CR  int `json:"cr"` // cache read
	CW  int `json:"cw"` // cache write
}

// ModelEvent captures a full model response: what it said + what it called + cost.
// Text is the model's prose output before/between tool calls — the decision trace.
type ModelEvent struct {
	Model  string    `json:"model"`
	Text   string    `json:"text,omitempty"`
	Calls  []CallRec `json:"calls,omitempty"`
	Tokens TokenRec  `json:"tokens"`
	Ms     int64     `json:"ms"`
}

// ToolExecEvent records a tool call being dispatched.
type ToolExecEvent struct {
	ID    string         `json:"id"`
	Name  string         `json:"name"`
	Input map[string]any `json:"input"`
}

// ToolDoneEvent records the tool result: timing, size, first 300 chars.
type ToolDoneEvent struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Ms      int64  `json:"ms"`
	Error   bool   `json:"error,omitempty"`
	Bytes   int    `json:"bytes"`
	Preview string `json:"preview,omitempty"`
}

// ── JSONL envelope ────────────────────────────────────────────────────────────

type entry struct {
	T    float64 `json:"t"`
	S    string  `json:"s"`
	Turn int     `json:"turn"`
	Step int     `json:"step"`
	Kind string  `json:"kind"`
	Data any     `json:"data"`
}

// ── Lifecycle ─────────────────────────────────────────────────────────────────

func New(id, cwd, model string) (*Session, error) {
	if id == "" {
		b := make([]byte, 6)
		if _, err := rand.Read(b); err != nil {
			return nil, fmt.Errorf("store: generate id: %w", err)
		}
		id = hex.EncodeToString(b)
	}
	dir := filepath.Join(sessionsDir, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("store: create dir: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(dir, "events.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("store: open log: %w", err)
	}
	s := &Session{
		ID: id, CWD: cwd, StartedAt: time.Now(), Model: model,
		f: f, start: time.Now(), dir: dir,
	}
	s.log(0, 0, "session_start", map[string]any{"cwd": cwd, "model": model})
	return s, nil
}

// ResumeOrNew opens an existing session by id or creates a new one.
// Returns the session and any previously saved messages.
func ResumeOrNew(id, cwd, model string) (*Session, []provider.Message, error) {
	if id != "" {
		dir := filepath.Join(sessionsDir, id)
		if _, err := os.Stat(dir); err == nil {
			f, err := os.OpenFile(filepath.Join(dir, "events.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			if err != nil {
				return nil, nil, fmt.Errorf("store: resume log: %w", err)
			}
			s := &Session{
				ID: id, CWD: cwd, StartedAt: time.Now(), Model: model,
				f: f, start: time.Now(), dir: dir,
			}
			s.log(0, 0, "session_resume", map[string]any{"cwd": cwd, "model": model})
			msgs, err := s.LoadMessages()
			if err != nil {
				return nil, nil, err
			}
			return s, msgs, nil
		}
	}
	s, err := New(id, cwd, model)
	if err != nil {
		return nil, nil, err
	}
	return s, nil, nil
}

func (s *Session) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.f != nil {
		s.f.Close()
		s.f = nil
	}
}

// Path returns the session directory (all session files live here).
func (s *Session) Path() string { return s.dir }

// ── Event logging ─────────────────────────────────────────────────────────────

// LogUser logs a user message and returns the turn number (1-based).
func (s *Session) LogUser(text string) int {
	s.mu.Lock()
	s.turnNum++
	turn := s.turnNum
	s.mu.Unlock()
	s.log(turn, 0, "user", map[string]any{"text": text, "chars": len(text)})
	return turn
}

func (s *Session) LogModel(turn, step int, e ModelEvent) {
	s.log(turn, step, "model", e)
}

func (s *Session) LogToolExec(turn, step int, e ToolExecEvent) {
	s.log(turn, step, "tool_exec", e)
}

func (s *Session) LogToolDone(turn, step int, e ToolDoneEvent) {
	s.log(turn, step, "tool_done", e)
}

func (s *Session) LogTurnEnd(turn, steps int, reason string) {
	s.log(turn, steps, "turn_end", map[string]any{"steps": steps, "reason": reason})
}

func (s *Session) LogCompaction(id, reason string) {
	s.log(0, 0, "compaction", map[string]any{"id": id, "reason": reason})
}

func (s *Session) log(turn, step int, kind string, data any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.f == nil {
		return
	}
	e := entry{
		T:    time.Since(s.start).Seconds(),
		S:    s.ID,
		Turn: turn,
		Step: step,
		Kind: kind,
		Data: data,
	}
	b, err := json.Marshal(e)
	if err != nil {
		return
	}
	s.f.Write(b)
	s.f.Write([]byte("\n"))
}

// ── Message persistence ───────────────────────────────────────────────────────

func (s *Session) SaveMessages(messages []provider.Message) error {
	path := filepath.Join(s.dir, "messages.json")
	data, err := json.MarshalIndent(messages, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func (s *Session) LoadMessages() ([]provider.Message, error) {
	path := filepath.Join(s.dir, "messages.json")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return []provider.Message{}, nil
	}
	if err != nil {
		return nil, err
	}
	var messages []provider.Message
	if err := json.Unmarshal(data, &messages); err != nil {
		fmt.Fprintf(os.Stderr, "store: warning: decode messages: %v\n", err)
		return []provider.Message{}, nil
	}
	return messages, nil
}

func (s *Session) MessagesCount() int {
	msgs, err := s.LoadMessages()
	if err != nil {
		return 0
	}
	return len(msgs)
}
