package reflect

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/trymimicode/mimicode-go/internal/provider"
	"github.com/trymimicode/mimicode-go/internal/store"
)

func RunReflect(ctx context.Context, sess *store.Session, cwd string) error {
	messages, err := sess.LoadMessages()
	if err != nil {
		warn("load messages: %v", err)
		return nil
	}
	if userTurnCount(messages) < 2 {
		return nil
	}

	prompt := "Summarize this coding session in 2-3 sentences. What was accomplished? What files changed? Any unresolved issues? Session transcript:\n" + flattenMessages(messages)
	response, _, err := provider.CallClaude(ctx, []provider.Message{{
		Role: "user",
		Content: []provider.ContentBlock{{
			Type: "text",
			Text: prompt,
		}},
	}}, "", nil, provider.ModelHaiku)
	if err != nil {
		warn("reflect call: %v", err)
		return nil
	}

	text := responseText(response)
	if text == "" {
		return nil
	}
	if err := appendMemory(cwd, sess.ID, text); err != nil {
		warn("append memory: %v", err)
	}
	return nil
}

func userTurnCount(messages []provider.Message) int {
	var count int
	for _, msg := range messages {
		if msg.Role == "user" && !isToolResultOnly(msg) {
			count++
		}
	}
	return count
}

func isToolResultOnly(msg provider.Message) bool {
	if len(msg.Content) == 0 {
		return false
	}
	for _, block := range msg.Content {
		if block.Type != "tool_result" {
			return false
		}
	}
	return true
}

func flattenMessages(messages []provider.Message) string {
	var b strings.Builder
	for _, msg := range messages {
		for _, block := range msg.Content {
			switch block.Type {
			case "text":
				fmt.Fprintf(&b, "[%s] %s\n", msg.Role, block.Text)
			case "tool_use":
				if path, ok := block.Input["path"].(string); ok && path != "" {
					fmt.Fprintf(&b, "[%s tool_use:%s] path=%s\n", msg.Role, block.Name, path)
					continue
				}
				input, _ := json.Marshal(block.Input)
				fmt.Fprintf(&b, "[%s tool_use:%s] %s\n", msg.Role, block.Name, truncate(string(input), 300))
			case "tool_result":
				fmt.Fprintf(&b, "[%s tool_result:%s] %s\n", msg.Role, block.ToolUseID, truncate(block.Content, 600))
			}
		}
	}
	return b.String()
}

func responseText(message provider.Message) string {
	var parts []string
	for _, block := range message.Content {
		if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
			parts = append(parts, strings.TrimSpace(block.Text))
		}
	}
	return strings.Join(parts, "\n")
}

func appendMemory(cwd, sessionID, text string) error {
	dir := filepath.Join(cwd, ".mimi")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, "MEMORY.md")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "## session-reflect: %s — %s\n%s\n\n", sessionID, time.Now().UTC().Format(time.RFC3339), text)
	return err
}

func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "... (truncated)"
}

func warn(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "reflect: warning: "+format+"\n", args...)
}
