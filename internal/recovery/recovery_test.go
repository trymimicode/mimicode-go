package recovery

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/trymimicode/mimicode-go/internal/provider"
	"github.com/trymimicode/mimicode-go/internal/store"
)

func TestDiagnoseParsesJSON(t *testing.T) {
	old := diagnose
	defer func() { diagnose = old }()

	var gotPrompt string
	diagnose = func(ctx context.Context, messages []provider.Message, system string, tools []provider.ToolSchema, model string) (provider.Message, provider.Usage, error) {
		gotPrompt = messages[0].Content[0].Text
		return provider.Message{
			Role: "assistant",
			Content: []provider.ContentBlock{{
				Type: "text",
				Text: "```json\n{\"went_wrong\":\"kept re-running a failing build\",\"instruction\":\"read the compile error first\",\"rule\":\"Read build errors before retrying.\"}\n```",
			}},
		}, provider.Usage{}, nil
	}

	sess, _, err := store.ResumeOrNew("rec-test", t.TempDir(), provider.ModelHaiku)
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	// Seed an event log so the tail reader has something to feed.
	_ = os.WriteFile(filepath.Join(sess.Path(), "events.jsonl"),
		[]byte(`{"kind":"model","data":{"text":"building"}}`+"\n"), 0o644)

	d, err := Diagnose(context.Background(), sess, "3 consecutive tool errors")
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if d.WentWrong == "" || d.Instruction == "" || d.Rule == "" {
		t.Fatalf("incomplete diagnosis: %+v", d)
	}
	if d.Rule != "Read build errors before retrying." {
		t.Fatalf("rule = %q", d.Rule)
	}
	if gotPrompt == "" {
		t.Fatal("expected prompt to include event log + reason")
	}
}

func TestDiagnoseFallsBackToRawText(t *testing.T) {
	old := diagnose
	defer func() { diagnose = old }()

	diagnose = func(ctx context.Context, messages []provider.Message, system string, tools []provider.ToolSchema, model string) (provider.Message, provider.Usage, error) {
		return provider.Message{
			Role:    "assistant",
			Content: []provider.ContentBlock{{Type: "text", Text: "not json, just prose"}},
		}, provider.Usage{}, nil
	}

	sess, _, _ := store.ResumeOrNew("rec-test2", t.TempDir(), provider.ModelHaiku)
	d, err := Diagnose(context.Background(), sess, "stuck")
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if d.WentWrong != "not json, just prose" {
		t.Fatalf("expected raw fallback, got %+v", d)
	}
}
