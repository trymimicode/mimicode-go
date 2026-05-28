package agent

import (
	"context"
	"testing"

	"github.com/trymimicode/mimicode-go/internal/provider"
)

func TestAgentTurnDetectsRepeatedToolCall(t *testing.T) {
	oldCall := callClaude
	defer func() { callClaude = oldCall }()

	// Model that always wants to read the same nonexistent file → identical,
	// erroring tool call every step.
	callClaude = func(ctx context.Context, messages []provider.Message, system string, tools []provider.ToolSchema, model string) (provider.Message, provider.Usage, error) {
		return provider.Message{
			Role: "assistant",
			Content: []provider.ContentBlock{{
				Type:  "tool_use",
				ID:    "tu",
				Name:  "read",
				Input: map[string]any{"path": "does-not-exist.xyz"},
			}},
		}, provider.Usage{}, nil
	}

	t.Setenv("MIMICODE_COMPACT_AUTO", "0")
	_, err := AgentTurn(context.Background(), AgentConfig{CWD: t.TempDir(), MaxSteps: 25}, "read it", nil)

	stuck, ok := IsStuck(err)
	if !ok {
		t.Fatalf("expected AgentStuck, got %v", err)
	}
	if stuck.Reason == "" {
		t.Fatal("stuck reason should be populated")
	}
}

func TestAgentTurnMaxStepsIsStuck(t *testing.T) {
	oldCall := callClaude
	defer func() { callClaude = oldCall }()

	// Vary the input each step so repeated-call detection never fires;
	// the loop should exhaust MaxSteps and report stuck.
	n := 0
	callClaude = func(ctx context.Context, messages []provider.Message, system string, tools []provider.ToolSchema, model string) (provider.Message, provider.Usage, error) {
		n++
		return provider.Message{
			Role: "assistant",
			Content: []provider.ContentBlock{{
				Type:  "tool_use",
				ID:    "tu",
				Name:  "bash",
				Input: map[string]any{"cmd": "echo step", "n": n},
			}},
		}, provider.Usage{}, nil
	}

	t.Setenv("MIMICODE_COMPACT_AUTO", "0")
	_, err := AgentTurn(context.Background(), AgentConfig{CWD: t.TempDir(), MaxSteps: 3}, "loop", nil)
	if _, ok := IsStuck(err); !ok {
		t.Fatalf("expected AgentStuck on max steps, got %v", err)
	}
}
