package watch

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/trymimicode/mimicode-go/internal/agent"
	"github.com/trymimicode/mimicode-go/internal/provider"
	"github.com/trymimicode/mimicode-go/internal/store"
)

// agentSession holds the live conversation state for one watch session.
type agentSession struct {
	sess     *store.Session
	messages []provider.Message
	cwd      string
}

// NewAgentBriefer returns a Briefer backed by the full agent (LLM + all tools).
// It opens or resumes a named session and keeps conversation history across turns.
// Also returns the session so the caller can reflect/log on shutdown.
func NewAgentBriefer(sessionID, cwd string) (Briefer, *store.Session, error) {
	sess, messages, err := store.ResumeOrNew(sessionID, cwd, provider.DefaultModel())
	if err != nil {
		return nil, nil, err
	}

	st := &agentSession{sess: sess, messages: messages, cwd: cwd}

	fn := func(ctx context.Context, dir, newContent string) (string, error) {
		prompt := strings.TrimSpace(newContent)
		if prompt == "" {
			return "", nil
		}
		next, err := agent.AgentTurn(ctx, agent.AgentConfig{
			CWD:      st.cwd,
			Session:  st.sess,
			MaxSteps: 25,
		}, prompt, st.messages)
		if err != nil {
			return "", err
		}
		st.messages = next
		_ = st.sess.SaveMessages(next)
		return buildResponse(turnMessages(next, prompt)), nil
	}

	return fn, sess, nil
}

// turnMessages returns the messages produced in this turn — everything after the
// user prompt we just sent. AgentTurn appends the prompt as a user text message,
// then the assistant/tool messages; but auto-compaction can replace earlier
// messages with a summary and shrink the slice, so we cannot slice by the prior
// length (that panics when len shrinks). Locating the prompt from the end is
// robust because compaction always preserves the most recent turns.
func turnMessages(msgs []provider.Message, prompt string) []provider.Message {
	want := strings.TrimSpace(prompt)
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m.Role != "user" {
			continue
		}
		for _, blk := range m.Content {
			if blk.Type == "text" && strings.TrimSpace(blk.Text) == want {
				return msgs[i+1:]
			}
		}
	}
	return msgs
}

// buildResponse formats the agent's turn as a readable block for code.mimi.
// The prose answer is primary; the tools mimi ran are collapsed into a single
// dim summary line rather than dumping every command and its full output (the
// full trace lives in the session's events.jsonl audit log).
func buildResponse(msgs []provider.Message) string {
	var actions []string
	var prose strings.Builder

	for _, msg := range msgs {
		if msg.Role != "assistant" {
			continue
		}
		for _, blk := range msg.Content {
			switch blk.Type {
			case "text":
				if t := strings.TrimSpace(blk.Text); t != "" {
					if prose.Len() > 0 {
						prose.WriteString("\n\n")
					}
					prose.WriteString(t)
				}
			case "tool_use":
				actions = append(actions, toolLabel(blk.Name, blk.Input))
			}
		}
	}

	var b strings.Builder
	if len(actions) > 0 {
		b.WriteString(clip("⋯ "+strings.Join(actions, " · "), 160))
		b.WriteString("\n\n")
	}
	b.WriteString(prose.String())
	return strings.TrimSpace(b.String())
}

// toolLabel renders one tool call as a short, scannable token for the action
// summary, e.g. "$ go test ./...", "read agent.go", or "search rate limiting".
func toolLabel(name string, input map[string]any) string {
	s := func(k string) string { v, _ := input[k].(string); return v }
	switch name {
	case "bash":
		return "$ " + clip(firstLine(s("cmd")), 72)
	case "read":
		return "read " + filepath.Base(s("path"))
	case "write":
		return "write " + filepath.Base(s("path"))
	case "edit":
		return "edit " + filepath.Base(s("path"))
	case "web_search", "stackoverflow_search":
		return "search " + clip(s("query"), 48)
	case "web_fetch":
		return "fetch " + clip(s("url"), 60)
	case "git_source":
		return "clone " + s("repo")
	case "memory_search":
		return "recall " + clip(s("query"), 40)
	case "memory_write":
		return "note"
	default:
		return name
	}
}

// firstLine returns the first non-empty line of s, trimmed.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

// clip truncates s to at most n runes, appending an ellipsis when it does.
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
