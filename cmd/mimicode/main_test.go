package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trymimicode/mimicode-go/internal/agent"
	"github.com/trymimicode/mimicode-go/internal/compactor"
	"github.com/trymimicode/mimicode-go/internal/provider"
	"github.com/trymimicode/mimicode-go/internal/router"
	"github.com/trymimicode/mimicode-go/internal/session"
)

func TestREPLSmokeWithFakeAgent(t *testing.T) {
	oldLookPath := lookPath
	oldGetenv := getenv
	oldGetwd := getwd
	oldResumeOrNew := resumeOrNew
	oldLoadMessages := loadMessages
	oldSaveMessages := saveMessages
	oldAgentTurn := agentTurn
	oldMaybeCompact := maybeCompact
	oldRunReflect := runReflect
	oldLastUsage := lastUsage
	oldRouteTurn := routeTurn
	defer func() {
		lookPath = oldLookPath
		getenv = oldGetenv
		getwd = oldGetwd
		resumeOrNew = oldResumeOrNew
		loadMessages = oldLoadMessages
		saveMessages = oldSaveMessages
		agentTurn = oldAgentTurn
		maybeCompact = oldMaybeCompact
		runReflect = oldRunReflect
		lastUsage = oldLastUsage
		routeTurn = oldRouteTurn
	}()

	tmp := t.TempDir()
	sess := session.Session{ID: "smoke", Path: filepath.Join(tmp, "smoke.jsonl")}
	var saved []provider.Message

	lookPath = func(file string) (string, error) { return file, nil }
	getenv = func(key string) string {
		if key == "ANTHROPIC_API_KEY" {
			return "test-key"
		}
		return os.Getenv(key)
	}
	getwd = func() (string, error) { return tmp, nil }
	resumeOrNew = func(sessionID string) session.Session { return sess }
	loadMessages = func(session.Session) ([]provider.Message, error) { return nil, nil }
	saveMessages = func(_ session.Session, messages []provider.Message) error {
		saved = append([]provider.Message(nil), messages...)
		return nil
	}
	agentTurn = func(ctx context.Context, cfg agent.AgentConfig, userMsg string, messages []provider.Message) ([]provider.Message, error) {
		messages = append(messages,
			provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: userMsg,
				}},
			},
			provider.Message{
				Role: "assistant",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: "fake response",
				}},
			},
		)
		return messages, nil
	}
	maybeCompact = func(ctx context.Context, messages []provider.Message, sessionPath string, lastTokensIn int) ([]provider.Message, *compactor.CompactionRecord, error) {
		return messages, nil, nil
	}
	runReflect = func(ctx context.Context, sessionID string, cwd string) error { return nil }
	lastUsage = func() provider.Usage { return provider.Usage{} }
	routeTurn = func(userText string) router.ModelChoice {
		return router.ModelChoice{Model: router.HAIKU, Reason: "test"}
	}

	var stdout, stderr bytes.Buffer
	code := runCLI(context.Background(), nil, strings.NewReader("hello\n:q\n"), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("runCLI exit code = %d, stderr:\n%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "fake response") {
		t.Fatalf("stdout missing fake response:\n%s", stdout.String())
	}
	if len(saved) != 2 {
		t.Fatalf("saved message count = %d, want 2", len(saved))
	}
	if !strings.Contains(stderr.String(), "[mimicode] REPL") {
		t.Fatalf("stderr missing REPL banner:\n%s", stderr.String())
	}
}
