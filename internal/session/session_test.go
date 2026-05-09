package session

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/trymimicode/mimicode-go/internal/provider"
)

func TestSaveLoadMessagesRoundTrip(t *testing.T) {
	s := Session{
		ID:   "test-session",
		Path: filepath.Join(t.TempDir(), "test-session.jsonl"),
	}
	messages := []provider.Message{
		{
			Role: "user",
			Content: []provider.ContentBlock{{
				Type: "text",
				Text: "hello",
			}},
		},
		{
			Role: "assistant",
			Content: []provider.ContentBlock{{
				Type: "text",
				Text: "hi",
			}},
		},
		{
			Role: "user",
			Content: []provider.ContentBlock{{
				Type:      "tool_result",
				ToolUseID: "tool-1",
				Content:   "done",
			}},
		},
	}

	if err := SaveMessages(s, messages); err != nil {
		t.Fatalf("SaveMessages: %v", err)
	}

	got, err := LoadMessages(s)
	if err != nil {
		t.Fatalf("LoadMessages: %v", err)
	}
	if !reflect.DeepEqual(got, messages) {
		t.Fatalf("round-trip mismatch:\ngot  %#v\nwant %#v", got, messages)
	}
	if count := MessagesCount(s); count != 3 {
		t.Fatalf("MessagesCount = %d, want 3", count)
	}
	if want := filepath.Join(filepath.Dir(s.Path), "test-session.messages.json"); MessagesPath(s) != want {
		t.Fatalf("MessagesPath = %q, want %q", MessagesPath(s), want)
	}
}
