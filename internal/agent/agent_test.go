package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/trymimicode/mimicode-go/internal/provider"
)

func TestAgentTurnToolLoopThenFinalResponse(t *testing.T) {
	oldCallClaude := callClaude
	oldCallClaudeStreaming := callClaudeStreaming
	defer func() {
		callClaude = oldCallClaude
		callClaudeStreaming = oldCallClaudeStreaming
	}()

	var calls int
	callClaude = func(ctx context.Context, messages []provider.Message, system string, tools []provider.ToolSchema, model string) (provider.Message, provider.Usage, error) {
		calls++
		if calls == 1 {
			return provider.Message{
				Role: "assistant",
				Content: []provider.ContentBlock{{
					Type:  "tool_use",
					ID:    "tu_1",
					Name:  "bash",
					Input: map[string]any{"cmd": "echo hello"},
				}},
			}, provider.Usage{}, nil
		}
		return provider.Message{
			Role: "assistant",
			Content: []provider.ContentBlock{{
				Type: "text",
				Text: "done",
			}},
		}, provider.Usage{}, nil
	}

	t.Setenv("MIMICODE_COMPACT_AUTO", "0")
	messages, err := AgentTurn(context.Background(), AgentConfig{
		CWD:      t.TempDir(),
		MaxSteps: 5,
	}, "please run echo", nil)
	if err != nil {
		t.Fatalf("AgentTurn: %v", err)
	}

	if len(messages) != 4 {
		t.Fatalf("message count = %d, want 4: %+v", len(messages), messages)
	}
	if messages[0].Role != "user" || messages[0].Content[0].Text != "please run echo" {
		t.Fatalf("message[0] = %+v, want user text", messages[0])
	}
	if messages[1].Role != "assistant" || len(messages[1].Content) != 1 || messages[1].Content[0].Type != "tool_use" {
		t.Fatalf("message[1] = %+v, want assistant tool_use", messages[1])
	}
	if messages[2].Role != "user" || len(messages[2].Content) != 1 || messages[2].Content[0].Type != "tool_result" {
		t.Fatalf("message[2] = %+v, want user tool_result", messages[2])
	}
	if messages[2].Content[0].ToolUseID != "tu_1" {
		t.Fatalf("tool result id = %q, want tu_1", messages[2].Content[0].ToolUseID)
	}
	if messages[3].Role != "assistant" || !strings.Contains(messages[3].Content[0].Text, "done") {
		t.Fatalf("message[3] = %+v, want assistant final", messages[3])
	}
	if calls != 2 {
		t.Fatalf("provider calls = %d, want 2", calls)
	}
}
