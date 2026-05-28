package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/trymimicode/mimicode-go/internal/provider"
)

// writeThenStop returns a model that asks to write out.txt once, then (after the
// tool result) finishes with a plain text reply.
func writeThenStop(t *testing.T) func(context.Context, []provider.Message, string, []provider.ToolSchema, string) (provider.Message, provider.Usage, error) {
	t.Helper()
	n := 0
	return func(ctx context.Context, _ []provider.Message, _ string, _ []provider.ToolSchema, _ string) (provider.Message, provider.Usage, error) {
		n++
		if n == 1 {
			return provider.Message{Role: "assistant", Content: []provider.ContentBlock{{
				Type: "tool_use", ID: "w1", Name: "write",
				Input: map[string]any{"path": "out.txt", "content": "hi"},
			}}}, provider.Usage{}, nil
		}
		return provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "ok"}}}, provider.Usage{}, nil
	}
}

func TestConfirmGateBlocksDeniedTool(t *testing.T) {
	old := callClaude
	defer func() { callClaude = old }()
	callClaude = writeThenStop(t)

	cwd := t.TempDir()
	t.Setenv("MIMICODE_COMPACT_AUTO", "0")
	msgs, err := AgentTurn(context.Background(), AgentConfig{
		CWD:         cwd,
		MaxSteps:    5,
		ConfirmTool: func(string, map[string]any) bool { return false },
	}, "write it", nil)
	if err != nil {
		t.Fatalf("AgentTurn: %v", err)
	}
	if _, e := os.Stat(filepath.Join(cwd, "out.txt")); !os.IsNotExist(e) {
		t.Fatal("denied write must not create the file")
	}
	if !hasBlockedResult(msgs) {
		t.Fatal("expected a blocked tool_result")
	}
}

func TestConfirmGateAllowsApprovedTool(t *testing.T) {
	old := callClaude
	defer func() { callClaude = old }()
	callClaude = writeThenStop(t)

	cwd := t.TempDir()
	t.Setenv("MIMICODE_COMPACT_AUTO", "0")
	_, err := AgentTurn(context.Background(), AgentConfig{
		CWD:         cwd,
		MaxSteps:    5,
		ConfirmTool: func(string, map[string]any) bool { return true },
	}, "write it", nil)
	if err != nil {
		t.Fatalf("AgentTurn: %v", err)
	}
	data, e := os.ReadFile(filepath.Join(cwd, "out.txt"))
	if e != nil {
		t.Fatalf("approved write should create the file: %v", e)
	}
	if string(data) != "hi" {
		t.Fatalf("file content = %q, want hi", data)
	}
}

func TestConfirmGateIgnoresReadOnlyTools(t *testing.T) {
	// read is not in gatedTools, so the gate must never be consulted for it.
	if gatedTools["read"] || gatedTools["web_search"] || gatedTools["memory_search"] {
		t.Fatal("read-only tools must not be gated")
	}
	if !gatedTools["bash"] || !gatedTools["write"] || !gatedTools["edit"] {
		t.Fatal("bash/write/edit must be gated")
	}
}

func hasBlockedResult(msgs []provider.Message) bool {
	for _, m := range msgs {
		for _, b := range m.Content {
			if b.Type == "tool_result" && b.IsError && len(b.Content) >= 7 && b.Content[:7] == "Blocked" {
				return true
			}
		}
	}
	return false
}
