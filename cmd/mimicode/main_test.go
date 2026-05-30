package main

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/trymimicode/mimicode-go/internal/agent"
	"github.com/trymimicode/mimicode-go/internal/compactor"
	"github.com/trymimicode/mimicode-go/internal/provider"
	"github.com/trymimicode/mimicode-go/internal/store"
)

type cliHooks struct {
	lookPath     func(string) (string, error)
	getenv       func(string) string
	getwd        func() (string, error)
	agentTurn    func(context.Context, agent.AgentConfig, string, []provider.Message) ([]provider.Message, error)
	maybeCompact func(context.Context, []provider.Message, string, int) ([]provider.Message, *compactor.CompactionRecord, error)
	runReflect   func(context.Context, *store.Session, string) error
	lastUsage    func() provider.Usage
}

func captureHooks() cliHooks {
	return cliHooks{
		lookPath:     lookPath,
		getenv:       getenv,
		getwd:        getwd,
		agentTurn:    agentTurn,
		maybeCompact: maybeCompact,
		runReflect:   runReflect,
		lastUsage:    lastUsage,
	}
}

func restoreHooks(h cliHooks) {
	lookPath = h.lookPath
	getenv = h.getenv
	getwd = h.getwd
	agentTurn = h.agentTurn
	maybeCompact = h.maybeCompact
	runReflect = h.runReflect
	lastUsage = h.lastUsage
}

func stubCommonCLI(t *testing.T) *store.Session {
	t.Helper()
	tmp := t.TempDir()
	sess, _, err := store.ResumeOrNew("stub", tmp, provider.ModelSonnet)
	if err != nil {
		t.Fatalf("create stub session: %v", err)
	}
	lookPath = func(file string) (string, error) { return file, nil }
	getenv = func(key string) string {
		if key == "ANTHROPIC_API_KEY" {
			return "test-key"
		}
		return os.Getenv(key)
	}
	getwd = func() (string, error) { return tmp, nil }
	maybeCompact = func(ctx context.Context, messages []provider.Message, sessionPath string, lastTokensIn int) ([]provider.Message, *compactor.CompactionRecord, error) {
		return messages, nil, nil
	}
	runReflect = func(context.Context, *store.Session, string) error { return nil }
	lastUsage = func() provider.Usage { return provider.Usage{} }
	return sess
}

func TestREPLSmokeWithFakeAgent(t *testing.T) {
	hooks := captureHooks()
	defer restoreHooks(hooks)

	stubCommonCLI(t)

	agentTurn = func(ctx context.Context, cfg agent.AgentConfig, userMsg string, messages []provider.Message) ([]provider.Message, error) {
		messages = append(messages,
			provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: userMsg}}},
			provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "fake response"}}},
		)
		return messages, nil
	}

	var stdout, stderr bytes.Buffer
	code := runCLI(context.Background(), []string{"--repl"}, strings.NewReader("hello\n:q\n"), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("runCLI exit code = %d, stderr:\n%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "fake response") {
		t.Fatalf("stdout missing fake response:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "[mimicode] REPL") {
		t.Fatalf("stderr missing REPL banner:\n%s", stderr.String())
	}
}

func TestCLIFlagsSessionAndPrompt(t *testing.T) {
	hooks := captureHooks()
	defer restoreHooks(hooks)

	stubCommonCLI(t)
	var gotPrompt string
	agentTurn = func(ctx context.Context, cfg agent.AgentConfig, userMsg string, messages []provider.Message) ([]provider.Message, error) {
		gotPrompt = userMsg
		return append(messages, provider.Message{
			Role:    "assistant",
			Content: []provider.ContentBlock{{Type: "text", Text: "ok"}},
		}), nil
	}

	var stdout, stderr bytes.Buffer
	code := runCLI(context.Background(), []string{"-s", "mysession", "prompt", "words"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("runCLI exit code = %d, stderr:\n%s", code, stderr.String())
	}
	if gotPrompt != "prompt words" {
		t.Fatalf("prompt = %q, want prompt words", gotPrompt)
	}
}

func TestCLIFlagsReplEntersREPL(t *testing.T) {
	hooks := captureHooks()
	defer restoreHooks(hooks)

	stubCommonCLI(t)
	agentTurn = func(ctx context.Context, cfg agent.AgentConfig, userMsg string, messages []provider.Message) ([]provider.Message, error) {
		t.Fatal("agentTurn should not be called when REPL immediately exits")
		return nil, nil
	}

	var stdout, stderr bytes.Buffer
	code := runCLI(context.Background(), []string{"--repl"}, strings.NewReader(":q\n"), &stdout, &stderr)
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
	out := stdout.String()
	if !strings.Contains(out, "mimicode version "+version) {
		t.Fatalf("version output = %q, want it to contain %q", out, "mimicode version "+version)
	}
	for _, field := range []string{"commit:", "built:", "go:"} {
		if !strings.Contains(out, field) {
			t.Errorf("version output missing %q field:\n%s", field, out)
		}
	}
}
