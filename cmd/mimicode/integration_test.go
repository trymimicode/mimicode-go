package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/trymimicode/mimicode-go/internal/agent"
	"github.com/trymimicode/mimicode-go/internal/logger"
	"github.com/trymimicode/mimicode-go/internal/provider"
)

func TestIntegrationAgentTurnReadsHelloGo(t *testing.T) {
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		t.Skip("ANTHROPIC_API_KEY is not set")
	}

	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "hello.go"), []byte(`package hello

func HelloFromIntegration() string {
	return "hello"
}
`), 0o644); err != nil {
		t.Fatalf("write hello.go: %v", err)
	}

	oldLogDir := logger.LOG_DIR
	logger.LOG_DIR = filepath.Join(cwd, ".mimi", "sessions")
	defer func() { logger.LOG_DIR = oldLogDir }()

	sessionID := "integration-" + time.Now().Format("20060102150405")
	if _, err := logger.StartSession(sessionID); err != nil {
		t.Fatalf("start session: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	messages, err := agent.AgentTurn(ctx, agent.AgentConfig{
		CWD:       cwd,
		MaxSteps:  8,
		SessionID: sessionID,
	}, "What functions are defined in hello.go?", nil)
	if err != nil {
		t.Fatalf("AgentTurn: %v", err)
	}

	if !containsToolUse(messages, "read") && !containsToolUse(messages, "bash") {
		t.Fatalf("expected read or bash tool_use in messages: %#v", messages)
	}
	if final := extractLastAssistantText(messages); !strings.Contains(final, "HelloFromIntegration") {
		t.Fatalf("final assistant text %q does not mention HelloFromIntegration", final)
	}

	matches, err := filepath.Glob(filepath.Join(cwd, ".mimi", "sessions", "*.jsonl"))
	if err != nil {
		t.Fatalf("glob session logs: %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("expected .mimi/sessions/*.jsonl file to be created")
	}
}

func containsToolUse(messages []provider.Message, name string) bool {
	for _, msg := range messages {
		for _, block := range msg.Content {
			if block.Type == "tool_use" && block.Name == name {
				return true
			}
		}
	}
	return false
}
