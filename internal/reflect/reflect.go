// Package reflect runs a post-session pass over the event-log decision trace.
// It summarizes what happened (→ MEMORY.md) and extracts durable behavioral
// rules to make future sessions better (→ RULES.md). It runs at session end,
// including when the session is interrupted, so it must use a fresh context.
package reflect

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/trymimicode/mimicode-go/internal/memory"
	"github.com/trymimicode/mimicode-go/internal/provider"
	"github.com/trymimicode/mimicode-go/internal/store"
)

const reflectPrompt = `You are reviewing a finished coding-agent session. Produce (1) a short summary and (2) durable behavioral rules that would help future sessions.

Be STRICT about rules: include one only if the log shows a real, repeatable mistake, inefficiency, or user correction worth preventing. Never repeat a rule that already exists.

Existing rules (do NOT repeat these):
%s

Session event log (each line is a step: model text + tool calls, a tool result with errors/timing, or a turn_end with its reason):
%s

Return ONLY a JSON object — no prose, no code fences:
{"summary": "<2-3 sentence recap, or empty string if nothing meaningful happened>", "rules": ["<imperative behavioral rule>", ...]}
Keep rules few and high-value (0-3). Use an empty array if none are warranted.`

const maxTraceBytes = 14_000

var callClaude = provider.CallClaude

type reflection struct {
	Summary string   `json:"summary"`
	Rules   []string `json:"rules"`
}

// RunReflect reads the session's event log and, if the session did real work,
// writes a summary to MEMORY.md and any extracted rules to RULES.md.
func RunReflect(ctx context.Context, sess *store.Session, cwd string) error {
	trace := readEventTail(sess.Path())
	// Skip trivial / pure-chat sessions: no tool calls means nothing to learn.
	if !strings.Contains(trace, `"kind":"tool_exec"`) {
		return nil
	}

	prompt := fmt.Sprintf(reflectPrompt, existingRulesOrNone(cwd), trace)
	resp, _, err := callClaude(ctx, []provider.Message{{
		Role:    "user",
		Content: []provider.ContentBlock{{Type: "text", Text: prompt}},
	}}, "", nil, provider.ModelHaiku)
	if err != nil {
		warn("reflect call: %v", err)
		return nil
	}

	r := parseReflection(responseText(resp))
	if r.Summary != "" {
		if err := appendMemory(cwd, sess.ID, r.Summary); err != nil {
			warn("append memory: %v", err)
		}
	}
	for _, rule := range r.Rules {
		if strings.TrimSpace(rule) == "" {
			continue
		}
		if err := memory.AppendRule(cwd, rule); err != nil {
			warn("append rule: %v", err)
		}
	}
	return nil
}

func parseReflection(raw string) reflection {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)

	var r reflection
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		// Fall back to treating the whole reply as a summary.
		return reflection{Summary: raw}
	}
	return r
}

func existingRulesOrNone(cwd string) string {
	if rules := strings.TrimSpace(memory.LoadRules(cwd)); rules != "" {
		return rules
	}
	return "(none yet)"
}

func readEventTail(sessionDir string) string {
	data, err := os.ReadFile(filepath.Join(sessionDir, "events.jsonl"))
	if err != nil {
		return ""
	}
	if len(data) > maxTraceBytes {
		data = data[len(data)-maxTraceBytes:]
		if i := strings.IndexByte(string(data), '\n'); i >= 0 {
			data = data[i+1:]
		}
	}
	return string(data)
}

func responseText(message provider.Message) string {
	var parts []string
	for _, block := range message.Content {
		if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
			parts = append(parts, strings.TrimSpace(block.Text))
		}
	}
	return strings.Join(parts, "\n")
}

func appendMemory(cwd, sessionID, text string) error {
	dir := filepath.Join(cwd, ".mimi")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, "MEMORY.md")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "## session-reflect: %s — %s\n%s\n\n", sessionID, time.Now().UTC().Format(time.RFC3339), text)
	return err
}

func warn(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "reflect: warning: "+format+"\n", args...)
}
