// Package recovery runs a clean-context diagnosis when the agent gets stuck.
// It reads the session event log (the decision trace), asks a fresh model call
// what went wrong, and proposes a recovery instruction plus a durable rule.
// It never rewrites code — only proposes a rule for .mimi/RULES.md.
package recovery

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/trymimicode/mimicode-go/internal/provider"
	"github.com/trymimicode/mimicode-go/internal/store"
)

const diagnosisPrompt = `A coding agent got stuck and stopped. Reason: %s

Below is the tail of its event log. Each "model" line is one reasoning step (its text + the tool calls it made); each "tool" line is a result, possibly an error. Diagnose the ROOT cause and how a fresh attempt should differ.

Return ONLY a JSON object — no prose, no code fences:
{"went_wrong": "<1-2 sentences: the root cause, concrete>", "instruction": "<a concrete, different approach for a clean retry>", "rule": "<one durable imperative behavioral rule to prevent this next time, or empty string if none is warranted>"}

Event log tail:
%s`

// maxLogTailBytes caps how much of the event log we feed the diagnosis call.
const maxLogTailBytes = 12_000

type Diagnosis struct {
	WentWrong   string `json:"went_wrong"`
	Instruction string `json:"instruction"`
	Rule        string `json:"rule"`
}

var diagnose = provider.CallClaude

// Diagnose reads the session's event log tail and asks Haiku what went wrong.
func Diagnose(ctx context.Context, sess *store.Session, reason string) (Diagnosis, error) {
	tail := readEventTail(sess.Path())
	prompt := fmt.Sprintf(diagnosisPrompt, reason, tail)

	msg, _, err := diagnose(ctx, []provider.Message{{
		Role:    "user",
		Content: []provider.ContentBlock{{Type: "text", Text: prompt}},
	}}, "", nil, provider.ModelHaiku)
	if err != nil {
		return Diagnosis{}, err
	}

	raw := firstText(msg)
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)

	var d Diagnosis
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		// Fall back to surfacing the raw text as the explanation.
		return Diagnosis{WentWrong: raw}, nil
	}
	return d, nil
}

// Format renders a diagnosis for display to the engineer.
func (d Diagnosis) Format() string {
	var b strings.Builder
	b.WriteString("\n⚠ mimi got stuck.\n")
	if d.WentWrong != "" {
		fmt.Fprintf(&b, "  what went wrong: %s\n", d.WentWrong)
	}
	if d.Instruction != "" {
		fmt.Fprintf(&b, "  recovery plan:   %s\n", d.Instruction)
	}
	if d.Rule != "" {
		fmt.Fprintf(&b, "  proposed rule:   %s\n", d.Rule)
	}
	return b.String()
}

func readEventTail(sessionDir string) string {
	data, err := os.ReadFile(filepath.Join(sessionDir, "events.jsonl"))
	if err != nil {
		return "(no event log available)"
	}
	if len(data) > maxLogTailBytes {
		data = data[len(data)-maxLogTailBytes:]
		// Drop a likely-partial first line.
		if i := strings.IndexByte(string(data), '\n'); i >= 0 {
			data = data[i+1:]
		}
	}
	return string(data)
}

func firstText(msg provider.Message) string {
	for _, b := range msg.Content {
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			return b.Text
		}
	}
	return ""
}
