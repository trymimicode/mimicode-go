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
		before := st.messages
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
		return buildResponse(before, next), nil
	}

	return fn, sess, nil
}

// buildResponse formats the agent's turn as a readable block for code.mimi.
// It shows each bash command and its output, then the final text response.
func buildResponse(before, after []provider.Message) string {
	// Map tool_use IDs → name so we can pair results with calls.
	type call struct{ name, cmd string }
	calls := map[string]call{}

	var b strings.Builder

	for _, msg := range after[len(before):] {
		switch msg.Role {
		case "assistant":
			for _, blk := range msg.Content {
				switch blk.Type {
				case "text":
					if t := strings.TrimSpace(blk.Text); t != "" {
						b.WriteString(t)
						b.WriteString("\n")
					}
				case "tool_use":
					c := call{name: blk.Name}
					if blk.Input != nil {
						c.cmd, _ = blk.Input["cmd"].(string)
					}
					calls[blk.ID] = c
					if blk.Name == "bash" && c.cmd != "" {
						b.WriteString("\n```\n$ ")
						b.WriteString(c.cmd)
						b.WriteString("\n")
					}
				}
			}
		case "user":
			for _, blk := range msg.Content {
				if blk.Type != "tool_result" {
					continue
				}
				c := calls[blk.ToolUseID]
				if c.name != "bash" {
					continue
				}
				content := strings.TrimRight(blk.Content, "\n")
				if blk.IsError {
					b.WriteString(content)
					b.WriteString("\n[non-zero exit]\n```\n\n")
				} else {
					b.WriteString(content)
					b.WriteString("\n```\n\n")
				}
			}
		}
	}

	return strings.TrimSpace(b.String())
}
