package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/trymimicode/mimicode-go/internal/provider"
)

// funcProvider is a test-only Provider backed by plain functions.
type funcProvider struct {
	call          func(ctx context.Context, messages []provider.Message, system string, tools []provider.ToolSchema, model string) (provider.Message, provider.Usage, error)
	callStreaming func(ctx context.Context, messages []provider.Message, system string, tools []provider.ToolSchema, model string, cb provider.StreamCallback) (provider.Message, provider.Usage, error)
}

func (p funcProvider) Call(ctx context.Context, messages []provider.Message, system string, tools []provider.ToolSchema, model string) (provider.Message, provider.Usage, error) {
	return p.call(ctx, messages, system, tools, model)
}

func (p funcProvider) CallStreaming(ctx context.Context, messages []provider.Message, system string, tools []provider.ToolSchema, model string, cb provider.StreamCallback) (provider.Message, provider.Usage, error) {
	if p.callStreaming != nil {
		return p.callStreaming(ctx, messages, system, tools, model, cb)
	}
	return p.call(ctx, messages, system, tools, model)
}

func (p funcProvider) DefaultModel() string { return "test-model" }

func TestAgentTurnToolLoopThenFinalResponse(t *testing.T) {
	var calls int
	prov := funcProvider{
		call: func(ctx context.Context, messages []provider.Message, system string, tools []provider.ToolSchema, model string) (provider.Message, provider.Usage, error) {
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
		},
	}

	t.Setenv("MIMICODE_COMPACT_AUTO", "0")
	messages, err := AgentTurn(context.Background(), AgentConfig{
		CWD:      t.TempDir(),
		MaxSteps: 5,
		Provider: prov,
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

// TestBuildSystemPersonaAndCacheBreak verifies the static persona is selected per
// provider and that the volatile context sits behind a cache-break marker.
func TestBuildSystemPersonaAndCacheBreak(t *testing.T) {
	cwd := t.TempDir()

	claude := BuildSystem(cwd, provider.Claude)
	if !strings.Contains(claude, provider.SystemCacheBreak) {
		t.Fatal("BuildSystem must embed the cache-break marker between persona and context")
	}
	persona, context, found := strings.Cut(claude, provider.SystemCacheBreak)
	if !found {
		t.Fatal("expected a single cache break")
	}
	if persona != SYSTEM_PROMPT {
		t.Errorf("Claude persona should be the lean SYSTEM_PROMPT, got %d bytes", len(persona))
	}
	if strings.Contains(persona, "Tool-call format") {
		t.Error("Claude persona must NOT carry the compat tool-format guide")
	}
	if !strings.Contains(context, "Current working directory") {
		t.Error("volatile context should hold the working directory")
	}

	// A non-Claude provider gets the compat persona with explicit tool formatting.
	compat := BuildSystem(cwd, provider.Kimi)
	cp, _, _ := strings.Cut(compat, provider.SystemCacheBreak)
	if !strings.Contains(cp, "Tool-call format") {
		t.Error("non-Claude persona must include the tool-format guide")
	}

	// nil provider defaults to Claude.
	if got := BuildSystem(cwd, nil); !strings.HasPrefix(got, SYSTEM_PROMPT) {
		t.Error("nil provider should default to the Claude persona")
	}
}
