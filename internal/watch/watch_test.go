package watch

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/trymimicode/mimicode-go/internal/provider"
)

func textMsg(role, text string) provider.Message {
	return provider.Message{Role: role, Content: []provider.ContentBlock{{Type: "text", Text: text}}}
}

// TestTurnMessagesSurvivesCompaction guards the crash where auto-compaction
// shrinks the message slice: slicing by the prior length panicked. turnMessages
// must locate the prompt from the end and return only this turn's output.
func TestTurnMessagesSurvivesCompaction(t *testing.T) {
	// Compaction replaced earlier turns with a single summary, so the slice is
	// short and the prompt sits near the end.
	msgs := []provider.Message{
		textMsg("user", "[summary of earlier conversation]"),
		textMsg("user", "the new question"),
		textMsg("assistant", "the answer"),
	}
	got := turnMessages(msgs, "  the new question  ")
	if len(got) != 1 || got[0].Role != "assistant" || got[0].Content[0].Text != "the answer" {
		t.Fatalf("turnMessages = %+v, want just the assistant answer", got)
	}
	// buildResponse must render it without panicking.
	if out := buildResponse(got); !strings.Contains(out, "the answer") {
		t.Fatalf("buildResponse = %q, want it to contain the answer", out)
	}
}

// TestTurnMessagesFallsBackWhenPromptMissing returns the whole slice rather than
// panicking when the prompt can't be found (e.g. it was itself compacted away).
func TestTurnMessagesFallsBackWhenPromptMissing(t *testing.T) {
	msgs := []provider.Message{textMsg("assistant", "orphaned answer")}
	got := turnMessages(msgs, "a prompt no longer present")
	if len(got) != 1 || got[0].Content[0].Text != "orphaned answer" {
		t.Fatalf("turnMessages fallback = %+v, want the full slice", got)
	}
}

func TestEnsureNotebookCreatesOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "code.mimi")
	if err := ensureNotebook(path); err != nil {
		t.Fatalf("ensureNotebook: %v", err)
	}
	if err := os.WriteFile(path, []byte("my own thoughts\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Second call must not clobber existing content.
	if err := ensureNotebook(path); err != nil {
		t.Fatalf("ensureNotebook (2): %v", err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "my own thoughts\n" {
		t.Errorf("ensureNotebook overwrote existing notebook: %q", string(data))
	}
}

func TestStampHeaderIfEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "code.mimi")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if !stampHeaderIfEmpty(path) {
		t.Fatal("expected header to be stamped into empty file")
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "tracking") {
		t.Errorf("tracking header not present: %q", string(data))
	}
	// A second call must be a no-op (file is no longer empty).
	if stampHeaderIfEmpty(path) {
		t.Error("header stamped twice; should be a no-op on non-empty file")
	}
}

func TestAppendResponseSeparatesAndReturnsSuffix(t *testing.T) {
	path := filepath.Join(t.TempDir(), "code.mimi")
	if err := os.WriteFile(path, []byte("question?\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	suffix, err := appendResponse(path, "  the answer  ")
	if err != nil {
		t.Fatalf("appendResponse: %v", err)
	}
	data, _ := os.ReadFile(path)
	got := string(data)
	if !strings.Contains(got, "question?") || !strings.Contains(got, "the answer") {
		t.Errorf("question or answer missing: %q", got)
	}
	// The returned suffix must be exactly what was appended.
	if !strings.HasSuffix(got, suffix) {
		t.Errorf("returned suffix %q is not the tail of the file %q", suffix, got)
	}
	if strings.Contains(got, "  the answer  ") {
		t.Errorf("answer was not trimmed: %q", got)
	}
}

// ── diff behaviour: append, mid-edit, clear+rewrite ──────────────────────────

func TestExtractNewContent(t *testing.T) {
	cases := []struct {
		name, old, new, want string
	}{
		{"append at end", "a\nb", "a\nb\nc", "c"},
		{"insert in middle", "a\nc", "a\nb\nc", "b"},
		{"clear and rewrite", "a\nb\nc", "totally new", "totally new"},
		{"no change", "a\nb", "a\nb", ""},
		{"whitespace only", "a\nb", "a\nb\n\n  ", ""},
		{"from empty", "", "first line", "first line"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := strings.TrimSpace(extractNewContent(tc.old, tc.new))
			if got != tc.want {
				t.Errorf("extractNewContent(%q,%q) = %q, want %q", tc.old, tc.new, got, tc.want)
			}
		})
	}
}

func TestNormalizeLF(t *testing.T) {
	if got := normalizeLF("a\r\nb\rc"); got != "a\nb\nc" {
		t.Errorf("normalizeLF = %q", got)
	}
}

// ── full loop integration ────────────────────────────────────────────────────

// fakeBriefer records every delta it is handed and returns a canned answer.
type fakeBriefer struct {
	mu     sync.Mutex
	deltas []string
	answer func(delta string) string
}

func (f *fakeBriefer) brief(_ context.Context, _, delta string) (string, error) {
	f.mu.Lock()
	f.deltas = append(f.deltas, delta)
	f.mu.Unlock()
	// Note: answer is invoked WITHOUT the lock held so a slow/blocking answer
	// (used to simulate "mimi is thinking") doesn't stall seen() in the test.
	if f.answer != nil {
		return f.answer(delta), nil
	}
	return "ANSWER:" + strings.TrimSpace(delta), nil
}

func (f *fakeBriefer) seen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.deltas...)
}

// waitFor polls cond until it is true or the deadline passes.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", what)
}

func startWatch(t *testing.T, fb *fakeBriefer) (dir, path string, cancel context.CancelFunc) {
	t.Helper()
	dir = t.TempDir()
	path = filepath.Join(dir, DefaultNotebook)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		_ = Run(ctx, Config{
			Dir:   dir,
			Poll:  15 * time.Millisecond,
			Brief: fb.brief,
		})
	}()
	// The header is stamped and the snapshot anchored on startup.
	// Wait for both so appendToFile in tests never races writeSnapshot.
	waitFor(t, "notebook created with header", func() bool {
		data, err := os.ReadFile(path)
		if err != nil || !strings.Contains(string(data), "tracking") {
			return false
		}
		_, snapErr := os.Stat(filepath.Join(dir, ".mimi", "watch.snapshot"))
		return snapErr == nil
	})
	return dir, path, cancel
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, _ := os.ReadFile(path)
	return string(data)
}

func appendToFile(t *testing.T, path, text string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(text); err != nil {
		t.Fatal(err)
	}
}

func TestRunHeaderAndAppendAnswer(t *testing.T) {
	fb := &fakeBriefer{}
	_, path, cancel := startWatch(t, fb)
	defer cancel()

	appendToFile(t, path, "what is 2+2?\n")
	waitFor(t, "answer appended", func() bool {
		return strings.Contains(readFile(t, path), "ANSWER:what is 2+2?")
	})

	// The header must never be sent to the briefer as input.
	for _, d := range fb.seen() {
		if strings.Contains(d, "tracking") {
			t.Errorf("header leaked into briefer delta: %q", d)
		}
	}
}

func TestRunDoesNotReplayItsOwnAnswer(t *testing.T) {
	fb := &fakeBriefer{}
	_, path, cancel := startWatch(t, fb)
	defer cancel()

	appendToFile(t, path, "first\n")
	waitFor(t, "first answered", func() bool {
		return strings.Contains(readFile(t, path), "ANSWER:first")
	})

	// Give the loop several ticks; mimi's own appended answer must not be
	// treated as new input and trigger another turn.
	time.Sleep(150 * time.Millisecond)
	if n := len(fb.seen()); n != 1 {
		t.Fatalf("expected exactly 1 turn, got %d: %v", n, fb.seen())
	}
}

func TestRunClearAndRewrite(t *testing.T) {
	fb := &fakeBriefer{}
	_, path, cancel := startWatch(t, fb)
	defer cancel()

	appendToFile(t, path, "original question\n")
	waitFor(t, "original answered", func() bool {
		return strings.Contains(readFile(t, path), "ANSWER:original question")
	})

	// Clear the whole file and write something brand new.
	if err := os.WriteFile(path, []byte("brand new question\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "rewrite answered", func() bool {
		return strings.Contains(readFile(t, path), "ANSWER:brand new question")
	})
}

func TestRunMidFileInsert(t *testing.T) {
	fb := &fakeBriefer{}
	_, path, cancel := startWatch(t, fb)
	defer cancel()

	appendToFile(t, path, "line a\nline c\n")
	// Wait for the first turn to fully land (answer written to the file), so the
	// snapshot is settled before we edit in place.
	waitFor(t, "initial answered", func() bool {
		return strings.Contains(readFile(t, path), "ANSWER:line a")
	})

	// Edit the real file in place: insert a line between "line a" and "line c",
	// leaving mimi's existing answer untouched.
	cur := readFile(t, path)
	edited := strings.Replace(cur, "line a\nline c", "line a\nline b inserted\nline c", 1)
	if edited == cur {
		t.Fatalf("failed to construct mid-file insert from: %q", cur)
	}
	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	waitFor(t, "inserted line answered", func() bool {
		for _, d := range fb.seen() {
			if strings.Contains(d, "line b inserted") {
				return true
			}
		}
		return false
	})
}

func TestRunEditsWhileThinkingAreNotLost(t *testing.T) {
	// The briefer blocks long enough for us to append more text mid-turn.
	release := make(chan struct{})
	var once sync.Once
	fb := &fakeBriefer{answer: func(delta string) string {
		if strings.Contains(delta, "slow") {
			<-release // hold the first turn open
		}
		return "ANSWER:" + strings.TrimSpace(delta)
	}}

	_, path, cancel := startWatch(t, fb)
	defer cancel()

	appendToFile(t, path, "slow question\n")
	// Wait until the briefer is actually processing the slow turn.
	waitFor(t, "slow turn started", func() bool {
		for _, d := range fb.seen() {
			if strings.Contains(d, "slow") {
				return true
			}
		}
		return false
	})

	// Type more while mimi is still thinking, then release it.
	appendToFile(t, path, "typed while thinking\n")
	once.Do(func() { close(release) })

	// The mid-thinking text must still get its own answer.
	waitFor(t, "mid-thinking edit answered", func() bool {
		return strings.Contains(readFile(t, path), "ANSWER:typed while thinking")
	})
}
