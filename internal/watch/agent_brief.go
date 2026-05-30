package watch

import (
	"context"
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
		return lastAssistantText(next), nil
	}

	return fn, sess, nil
}

func lastAssistantText(messages []provider.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != "assistant" {
			continue
		}
		var parts []string
		for _, b := range messages[i].Content {
			if b.Type == "text" && b.Text != "" {
				parts = append(parts, b.Text)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "\n")
		}
	}
	return ""
}
