package compactor

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trymimicode/mimicode-go/internal/provider"
)

func TestFindSplit(t *testing.T) {
	messages := conversation(5)

	split := findSplit(messages, 2)
	if split != 6 {
		t.Fatalf("findSplit = %d, want 6", split)
	}

	if split := findSplit(messages, 5); split != 0 {
		t.Fatalf("findSplit with all turns kept = %d, want 0", split)
	}
}

func TestShouldAutoCompactThresholds(t *testing.T) {
	t.Setenv("MIMICODE_COMPACT_AUTO", "0")
	if ok, reason := ShouldAutoCompact(conversation(8), 50000); ok || reason != "" {
		t.Fatalf("disabled auto compact = %v, %q; want false", ok, reason)
	}

	t.Setenv("MIMICODE_COMPACT_AUTO", "1")
	t.Setenv("MIMICODE_COMPACT_TURN_INTERVAL", "2")
	ok, reason := ShouldAutoCompact(conversation(5), 0)
	if !ok || !strings.HasPrefix(reason, "turn_interval:") {
		t.Fatalf("turn threshold = %v, %q; want turn interval compaction", ok, reason)
	}

	t.Setenv("MIMICODE_COMPACT_TURN_INTERVAL", "100")
	t.Setenv("MIMICODE_COMPACT_TOKEN_THRESHOLD", "100")
	ok, reason = ShouldAutoCompact(conversation(4), 100)
	if !ok || !strings.HasPrefix(reason, "token_threshold:") {
		t.Fatalf("token threshold = %v, %q; want token threshold compaction", ok, reason)
	}
}

func TestFormatMarker(t *testing.T) {
	marker := formatMarker("c007", map[string]any{"one_line": "summarized the setup"}, [2]int{2, 4})
	for _, want := range []string{
		"[COMPACTED",
		"turns 2",
		"4",
		"id=c007",
		"summarized the setup",
	} {
		if !strings.Contains(marker, want) {
			t.Fatalf("marker missing %q:\n%s", want, marker)
		}
	}
}

func TestCompactShortensMessagesAndAddsMarker(t *testing.T) {
	oldSummarize := summarizeTranscript
	summarizeTranscript = func(ctx context.Context, transcript string) map[string]any {
		return map[string]any{
			"one_line":      "test summary",
			"user_intents":  []any{"build compactor"},
			"decisions":     []any{},
			"files_touched": []any{},
			"tools_used":    map[string]any{},
			"key_findings":  []any{},
			"open_issues":   []any{},
		}
	}
	defer func() { summarizeTranscript = oldSummarize }()

	messages := conversation(6)
	next, record, err := Compact(context.Background(), messages, filepath.Join(t.TempDir(), "session.jsonl"), 2, "test")
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if record == nil {
		t.Fatal("expected compaction record")
	}
	if len(next) >= len(messages) {
		t.Fatalf("Compact did not shorten messages: got %d, original %d", len(next), len(messages))
	}
	if !isMarker(next[0]) {
		t.Fatalf("first compacted message is not a marker: %+v", next[0])
	}
	if record.ID != "c001" || record.MsgCount == 0 {
		t.Fatalf("unexpected record: %+v", record)
	}
}

func conversation(userTurns int) []provider.Message {
	messages := make([]provider.Message, 0, userTurns*2)
	for i := 0; i < userTurns; i++ {
		messages = append(messages,
			provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: "user turn",
				}},
			},
			provider.Message{
				Role: "assistant",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: "assistant turn",
				}},
			},
		)
	}
	return messages
}
