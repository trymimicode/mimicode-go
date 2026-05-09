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

type cliHooks struct {
	lookPath     func(string) (string, error)
	getenv       func(string) string
	getwd        func() (string, error)
	resumeOrNew  func(string) session.Session
	loadMessages func(session.Session) ([]provider.Message, error)
	saveMessages func(session.Session, []provider.Message) error
	agentTurn    func(context.Context, agent.AgentConfig, string, []provider.Message) ([]provider.Message, error)
	maybeCompact func(context.Context, []provider.Message, string, int) ([]provider.Message, *compactor.CompactionRecord, error)
	runReflect   func(context.Context, string, string) error
	lastUsage    func() provider.Usage
	routeTurn    func(string) router.ModelChoice
}

func captureHooks() cliHooks {
	return cliHooks{
		lookPath:     lookPath,
		getenv:       getenv,
		getwd:        getwd,
		resumeOrNew:  resumeOrNew,
		loadMessages: loadMessages,
		saveMessages: saveMessages,
		agentTurn:    agentTurn,
		maybeCompact: maybeCompact,
		runReflect:   runReflect,
		lastUsage:    lastUsage,
		routeTurn:    routeTurn,
	}
}

func restoreHooks(h cliHooks) {
	lookPath = h.lookPath
	getenv = h.getenv
	getwd = h.getwd
	resumeOrNew = h.resumeOrNew
	loadMessages = h.loadMessages
	saveMessages = h.saveMessages
	agentTurn = h.agentTurn
	maybeCompact = h.maybeCompact
	runReflect = h.runReflect
	lastUsage = h.lastUsage
	routeTurn = h.routeTurn
}

func stubCommonCLI(t *testing.T) session.Session {
	t.Helper()

	tmp := t.TempDir()
	sess := session.Session{ID: "stub", Path: filepath.Join(tmp, "stub.jsonl")}
	lookPath = func(file string) (string, error) { return file, nil }
	getenv = func(key string) string {
		if key == "ANTHROPIC_API_KEY" {
			return "test-key"
		}
		return os.Getenv(key)
	}
	getwd = func() (string, error) { return tmp, nil }
	loadMessages = func(session.Session) ([]provider.Message, error) { return nil, nil }
	saveMessages = func(session.Session, []provider.Message) error { return nil }
	maybeCompact = func(ctx context.Context, messages []provider.Message, sessionPath string, lastTokensIn int) ([]provider.Message, *compactor.CompactionRecord, error) {
		return messages, nil, nil
	}
	runReflect = func(context.Context, string, string) error { return nil }
	lastUsage = func() provider.Usage { return provider.Usage{} }
	routeTurn = func(string) router.ModelChoice {
		return router.ModelChoice{Model: router.HAIKU, Reason: "test"}
	}
	return sess
}

func TestREPLSmokeWithFakeAgent(t *testing.T) {
	hooks := captureHooks()
	defer restoreHooks(hooks)

	sess := stubCommonCLI(t)
	sess.ID = "smoke"
	var saved []provider.Message

	resumeOrNew = func(sessionID string) session.Session { return sess }
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

func TestCLIFlagsSessionAndPrompt(t *testing.T) {
	hooks := captureHooks()
	defer restoreHooks(hooks)

	sess := stubCommonCLI(t)
	var gotSessionID string
	var gotPrompt string
	resumeOrNew = func(sessionID string) session.Session {
		gotSessionID = sessionID
		sess.ID = sessionID
		return sess
	}
	agentTurn = func(ctx context.Context, cfg agent.AgentConfig, userMsg string, messages []provider.Message) ([]provider.Message, error) {
		gotPrompt = userMsg
		if cfg.SessionID != "mysession" {
			t.Fatalf("cfg.SessionID = %q, want mysession", cfg.SessionID)
		}
		return append(messages, provider.Message{
			Role: "assistant",
			Content: []provider.ContentBlock{{
				Type: "text",
				Text: "ok",
			}},
		}), nil
	}

	var stdout, stderr bytes.Buffer
	code := runCLI(context.Background(), []string{"-s", "mysession", "prompt", "words"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("runCLI exit code = %d, stderr:\n%s", code, stderr.String())
	}
	if gotSessionID != "mysession" {
		t.Fatalf("session id = %q, want mysession", gotSessionID)
	}
	if gotPrompt != "prompt words" {
		t.Fatalf("prompt = %q, want prompt words", gotPrompt)
	}
}

func TestCLIFlagsNoPromptEntersREPL(t *testing.T) {
	hooks := captureHooks()
	defer restoreHooks(hooks)

	sess := stubCommonCLI(t)
	resumeOrNew = func(sessionID string) session.Session { return sess }
	agentTurn = func(ctx context.Context, cfg agent.AgentConfig, userMsg string, messages []provider.Message) ([]provider.Message, error) {
		t.Fatal("agentTurn should not be called when REPL immediately exits")
		return nil, nil
	}

	var stdout, stderr bytes.Buffer
	code := runCLI(context.Background(), nil, strings.NewReader(":q\n"), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("runCLI exit code = %d, stderr:\n%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "[mimicode] REPL") {
		t.Fatalf("stderr missing REPL banner:\n%s", stderr.String())
	}
}

func TestCLIFlagsVersion(t *testing.T) {
	hooks := captureHooks()
	defer restoreHooks(hooks)

	lookPath = func(string) (string, error) {
		t.Fatal("startup checks should not run for --version")
		return "", nil
	}
	getenv = func(string) string {
		t.Fatal("startup checks should not run for --version")
		return ""
	}

	var stdout, stderr bytes.Buffer
	code := runCLI(context.Background(), []string{"--version"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("runCLI exit code = %d, stderr:\n%s", code, stderr.String())
	}
	if strings.TrimSpace(stdout.String()) != version {
		t.Fatalf("version output = %q, want %q", stdout.String(), version)
	}
}
