package reflect

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trymimicode/mimicode-go/internal/provider"
	"github.com/trymimicode/mimicode-go/internal/store"
)

func newSession(t *testing.T, id, cwd, events string) *store.Session {
	t.Helper()
	sess, _, err := store.ResumeOrNew(id, cwd, provider.ModelHaiku)
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(sess.Path()) })
	if events != "" {
		if err := os.WriteFile(filepath.Join(sess.Path(), "events.jsonl"), []byte(events), 0o644); err != nil {
			t.Fatalf("write events: %v", err)
		}
	}
	return sess
}

func TestReflectWritesSummaryAndRules(t *testing.T) {
	old := callClaude
	defer func() { callClaude = old }()
	callClaude = func(ctx context.Context, _ []provider.Message, _ string, _ []provider.ToolSchema, _ string) (provider.Message, provider.Usage, error) {
		return provider.Message{Role: "assistant", Content: []provider.ContentBlock{{
			Type: "text",
			Text: `{"summary":"Made the project prod-ready.","rules":["When grep exits non-zero it means no match, not a failure — do not re-run to double-check."]}`,
		}}}, provider.Usage{}, nil
	}

	cwd := t.TempDir()
	sess := newSession(t, "reflect-1", cwd, `{"kind":"tool_exec","data":{"name":"bash"}}`+"\n")

	if err := RunReflect(context.Background(), sess, cwd); err != nil {
		t.Fatalf("RunReflect: %v", err)
	}

	mem, _ := os.ReadFile(filepath.Join(cwd, ".mimi", "MEMORY.md"))
	if !strings.Contains(string(mem), "prod-ready") {
		t.Fatalf("MEMORY.md missing summary: %q", mem)
	}
	rules, _ := os.ReadFile(filepath.Join(cwd, ".mimi", "RULES.md"))
	if !strings.Contains(string(rules), "grep exits non-zero") {
		t.Fatalf("RULES.md missing rule: %q", rules)
	}
}

func TestReflectSkipsSessionsWithoutToolActivity(t *testing.T) {
	old := callClaude
	defer func() { callClaude = old }()
	called := false
	callClaude = func(ctx context.Context, _ []provider.Message, _ string, _ []provider.ToolSchema, _ string) (provider.Message, provider.Usage, error) {
		called = true
		return provider.Message{}, provider.Usage{}, nil
	}

	cwd := t.TempDir()
	// Pure chat: a user message and a model reply, but no tool_exec.
	events := `{"kind":"user","data":{"text":"hi"}}` + "\n" + `{"kind":"model","data":{"text":"hello"}}` + "\n"
	sess := newSession(t, "reflect-2", cwd, events)

	if err := RunReflect(context.Background(), sess, cwd); err != nil {
		t.Fatalf("RunReflect: %v", err)
	}
	if called {
		t.Fatal("reflect should skip the model call when there was no tool activity")
	}
	if _, err := os.Stat(filepath.Join(cwd, ".mimi", "RULES.md")); !os.IsNotExist(err) {
		t.Fatal("no RULES.md should be written for a pure-chat session")
	}
}

func TestReflectNoRulesWhenModelReturnsEmpty(t *testing.T) {
	old := callClaude
	defer func() { callClaude = old }()
	callClaude = func(ctx context.Context, _ []provider.Message, _ string, _ []provider.ToolSchema, _ string) (provider.Message, provider.Usage, error) {
		return provider.Message{Role: "assistant", Content: []provider.ContentBlock{{
			Type: "text", Text: `{"summary":"Read some files.","rules":[]}`,
		}}}, provider.Usage{}, nil
	}

	cwd := t.TempDir()
	sess := newSession(t, "reflect-3", cwd, `{"kind":"tool_exec","data":{"name":"read"}}`+"\n")

	if err := RunReflect(context.Background(), sess, cwd); err != nil {
		t.Fatalf("RunReflect: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cwd, ".mimi", "RULES.md")); !os.IsNotExist(err) {
		t.Fatal("empty rules array must not create RULES.md")
	}
}
