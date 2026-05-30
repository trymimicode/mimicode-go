// Package watch runs mimi as an ambient assistant in a folder.  It watches a
// plain-text notebook (code.mimi) the engineer thinks out loud in.  When new
// content is saved the agent reads it, responds, and appends the response to
// the end of the same file — pure append, no zones, no markers required.
//
// The Briefer and Injector seams are kept for extensibility and tests.
package watch

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/trymimicode/mimicode-go/internal/gitsource"
	"github.com/trymimicode/mimicode-go/internal/repomap"
)

// DefaultNotebook is the think-out-loud file created in the watched folder.
const DefaultNotebook = "code.mimi"

// Briefer turns new content the user wrote into a response string.
// dir is the watched folder; newContent is the text added since the last turn.
type Briefer func(ctx context.Context, dir, newContent string) (string, error)

// Injector surfaces a response to the engineer.  It receives the notebook path
// and the response text.  The default (appendResponse) appends at the end of the
// file.  A custom implementation must not disturb the engineer's own text.
type Injector func(notebookPath, response string) error

// Config configures a watch session.  Dir is required; everything else has a
// sensible default.
type Config struct {
	Dir          string
	NotebookPath string        // default: <Dir>/code.mimi
	Poll         time.Duration // default: 700ms
	Brief        Briefer       // default: defaultBrief (no-LLM local gather)
	Inject       Injector      // default: nil (appendResponse used directly for offset tracking)
	Out          io.Writer     // status messages; default: os.Stderr
}

// BriefMarker is kept for backward-compat / old tests.
const BriefMarker = "=== mimi ==="

// RunBackground watches dir for code.mimi to appear. The moment the file is
// created (by the user, by any means), it writes a confirmation header, anchors
// the processed offset after that header, then runs the normal change-detection
// loop. Designed to be called as "go watch.RunBackground(ctx, dir)".
func RunBackground(ctx context.Context, dir string) {
	notebookPath := filepath.Join(dir, DefaultNotebook)

	// If the file already existed before we started, jump straight into watching.
	// Otherwise poll until it appears.
	if _, err := os.Stat(notebookPath); err != nil {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
	waitLoop:
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := os.Stat(notebookPath); err == nil {
					break waitLoop
				}
			}
		}
	}

	// File exists. Write the confirmation header if the file is empty.
	writeWelcomeIfEmpty(notebookPath)

	// Wipe any stale lock so the offset is re-anchored after our header write.
	clearLock(dir)

	briefer, _, _ := NewAgentBriefer("code-mimi", dir)

	cfg := Config{
		Dir:          dir,
		NotebookPath: notebookPath,
		Brief:        briefer,        // nil falls back to defaultBrief (no-LLM)
		Out:          io.Discard,     // silent — changes are visible in the file
	}
	_ = Run(ctx, cfg)
}

// Run sets up the folder and watches the notebook until ctx is cancelled.
// It uses offset-based change detection persisted in .mimi/watch.lock, so
// the agent never replays content it already responded to across restarts.
func Run(ctx context.Context, cfg Config) error {
	cfg = withDefaults(cfg)

	if info, err := os.Stat(cfg.Dir); err != nil || !info.IsDir() {
		return fmt.Errorf("watch: %s is not a directory", cfg.Dir)
	}
	if err := os.MkdirAll(gitsource.CacheRoot(cfg.Dir), 0o755); err != nil {
		return fmt.Errorf("watch: create cache dir: %w", err)
	}
	repomap.Init(cfg.Dir)

	if err := ensureNotebook(cfg.NotebookPath); err != nil {
		return fmt.Errorf("watch: create notebook: %w", err)
	}

	// Ensure .mimi.lock is gitignored (best-effort).
	ensureGitignored(cfg.Dir, ".mimi/watch.lock")

	state := readState(cfg.Dir)

	// Clear a stale Processing flag left by a crashed previous run so the loop
	// doesn't skip every tick forever.
	if state.Processing {
		state.Processing = false
		writeState(cfg.Dir, state)
	}

	// On first run (no prior state), anchor the offset at the current end of the
	// file so we don't fire on existing content.
	if state.ProcessedOffset == 0 {
		if info, err := os.Stat(cfg.NotebookPath); err == nil {
			state.ProcessedOffset = info.Size()
			writeState(cfg.Dir, state)
		}
	}

	fmt.Fprintf(cfg.Out, "[mimi] watching %s\n", cfg.Dir)
	fmt.Fprintf(cfg.Out, "[mimi] write at the end of %s — save to send\n", cfg.NotebookPath)

	ticker := time.NewTicker(cfg.Poll)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			fmt.Fprintln(cfg.Out, "\n[mimi] stopped")
			return nil

		case <-ticker.C:
			if state.Processing {
				continue
			}

			info, err := os.Stat(cfg.NotebookPath)
			if err != nil {
				continue
			}
			// File was truncated (e.g. user cleared it). Reset offset so new writes fire.
			if info.Size() < state.ProcessedOffset {
				state.ProcessedOffset = info.Size()
				writeState(cfg.Dir, state)
				continue
			}
			if info.Size() == state.ProcessedOffset {
				continue
			}

			delta, err := readDelta(cfg.NotebookPath, state.ProcessedOffset)
			if err != nil {
				fmt.Fprintf(cfg.Out, "[mimi] read error: %v\n", err)
				continue
			}
			if strings.TrimSpace(delta) == "" {
				// Only whitespace was appended; advance offset and wait.
				state.ProcessedOffset = info.Size()
				writeState(cfg.Dir, state)
				continue
			}

			fmt.Fprintf(cfg.Out, "[mimi] new input detected (%d bytes)\n", len(delta))

			state.Processing = true
			writeState(cfg.Dir, state)

			// Write a thinking indicator so the user sees activity in their editor.
			responseStart := appendThinkingIndicator(cfg.NotebookPath)

			response, briefErr := cfg.Brief(ctx, cfg.Dir, delta)

			// Remove thinking indicator before writing the real response.
			_ = os.Truncate(cfg.NotebookPath, responseStart)

			if briefErr != nil {
				fmt.Fprintf(cfg.Out, "[mimi] brief error: %v\n", briefErr)
				errLine := fmt.Sprintf("[mimi error: %v]", briefErr)
				newOffset, _ := appendResponseRaw(cfg.NotebookPath, errLine)
				state.ProcessedOffset = newOffset
				state.Processing = false
				writeState(cfg.Dir, state)
				continue
			}

			if strings.TrimSpace(response) == "" {
				state.ProcessedOffset = responseStart
				state.Processing = false
				writeState(cfg.Dir, state)
				continue
			}

			// Use custom injector if provided; otherwise use built-in append.
			var newOffset int64
			if cfg.Inject != nil {
				_ = cfg.Inject(cfg.NotebookPath, response)
				if fi, err := os.Stat(cfg.NotebookPath); err == nil {
					newOffset = fi.Size()
				}
			} else {
				newOffset, _ = appendResponseRaw(cfg.NotebookPath, response)
			}

			state.ProcessedOffset = newOffset
			state.Processing = false
			writeState(cfg.Dir, state)

			fmt.Fprintf(cfg.Out, "[mimi] response appended (%d bytes)\n", newOffset-responseStart)
		}
	}
}

// ── File helpers ─────────────────────────────────────────────────────────────

// readDelta reads the content added to path after the given byte offset.
func readDelta(path string, offset int64) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return "", err
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// appendThinkingIndicator writes a visible "thinking" line to the end of the file
// and returns the file size BEFORE the indicator (the truncation point).
func appendThinkingIndicator(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	start := info.Size()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return start
	}
	defer f.Close()
	fmt.Fprint(f, "\n\n·· mimi thinking ··\n")
	return start
}

// appendResponseRaw appends the response with surrounding blank lines and
// returns the new file size (which becomes the next ProcessedOffset).
func appendResponseRaw(path, response string) (int64, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "\n\n%s\n\n", strings.TrimSpace(response))
	if err != nil {
		return 0, err
	}
	info, err := f.Stat()
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

// writeWelcomeIfEmpty writes the confirmation header only when the file is
// brand-new (empty). Existing content is never touched.
func writeWelcomeIfEmpty(path string) {
	info, err := os.Stat(path)
	if err != nil || info.Size() > 0 {
		return
	}
	header := "mimicode is watching ✓\n" +
		"write below, save to send — responses appear below your text.\n" +
		"\n"
	_ = os.WriteFile(path, []byte(header), 0o644)
}

// clearLock removes the watch lock so Run re-anchors the offset from scratch.
// Called after writeWelcomeIfEmpty so the header is excluded from prompts.
func clearLock(dir string) {
	_ = os.Remove(stateLockPath(dir))
}

// ensureGitignored adds entry to .gitignore if not present (best-effort).
func ensureGitignored(dir, entry string) {
	giPath := filepath.Join(dir, ".gitignore")
	data, _ := os.ReadFile(giPath)
	if strings.Contains(string(data), entry) {
		return
	}
	f, err := os.OpenFile(giPath, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "\n# mimicode watch state\n%s\n", entry)
}

// ── Config defaults ───────────────────────────────────────────────────────────

func withDefaults(cfg Config) Config {
	if cfg.NotebookPath == "" {
		cfg.NotebookPath = filepath.Join(cfg.Dir, DefaultNotebook)
	}
	if cfg.Poll <= 0 {
		cfg.Poll = 700 * time.Millisecond
	}
	if cfg.Brief == nil {
		cfg.Brief = defaultBrief
	}
	if cfg.Out == nil {
		cfg.Out = os.Stderr
	}
	return cfg
}

func ensureNotebook(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	return os.WriteFile(path, []byte(""), 0o644)
}

// ── Backward-compat helpers (kept for existing tests) ────────────────────────

// readThinking returns the engineer's portion of the notebook (everything before
// the first BriefMarker).
func readThinking(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	text := string(data)
	if i := strings.Index(text, BriefMarker); i >= 0 {
		text = text[:i]
	}
	return strings.TrimRight(text, "\n "), true
}

// appendBrief replaces any prior injected brief with the new one, keeping the
// engineer's text untouched above the marker.
func appendBrief(notebookPath, brief string) error {
	thinking, ok := readThinking(notebookPath)
	if !ok {
		return fmt.Errorf("read notebook")
	}
	block := fmt.Sprintf("%s\n%s\n%s\n", thinking, BriefMarker, strings.TrimSpace(brief))
	return os.WriteFile(notebookPath, []byte(block), 0o644)
}

// defaultBrief is a local, no-LLM gather used when no Briefer is configured.
func defaultBrief(ctx context.Context, dir, notebook string) (string, error) {
	var b strings.Builder
	b.WriteString("(local gather — run with -agent to use the LLM)\n")

	if rm := repomap.Cached(); rm != "" {
		if len(rm) > 1200 {
			rm = rm[:1200] + "\n... (truncated)"
		}
		b.WriteString("\nrepo shape:\n")
		b.WriteString(rm)
	}
	if repos := gitsource.List(dir); len(repos) > 0 {
		b.WriteString("\n\ncached source:")
		for _, r := range repos {
			fmt.Fprintf(&b, "\n  %s", r.LocalPath)
		}
	}
	return b.String(), nil
}
