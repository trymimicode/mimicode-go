// Package watch runs mimi as an ambient assistant inside a folder.
//
// It tracks a plain-text notebook (code.mimi) the engineer thinks out loud in.
// The moment the file is created it stamps a "tracking" header on top. From
// then on, every time the engineer writes something and saves, mimi reads what
// changed, responds, and appends the answer directly below — in the same file.
//
// Detection is diff-based, like `git diff`: each save is compared line-by-line
// against a snapshot of everything mimi has already accounted for. That makes
// it robust no matter how the engineer edits — appending at the end, inserting
// between existing lines, or clearing the whole file and starting fresh all
// produce the right delta, and mimi never replays its own appended answers.
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

// DefaultNotebook is the think-out-loud file tracked in the watched folder.
const DefaultNotebook = "code.mimi"

// Briefer turns the text the engineer just added into a response.
// dir is the watched folder; delta is the content added since the last turn.
type Briefer func(ctx context.Context, dir, delta string) (string, error)

// Config configures a watch session. Dir is required; everything else defaults.
type Config struct {
	Dir          string
	NotebookPath string        // default: <Dir>/code.mimi
	Poll         time.Duration // default: 500ms
	Brief        Briefer       // default: defaultBrief (no-LLM placeholder)
	Out          io.Writer     // status log; default: os.Stderr
}

// RunBackground waits for code.mimi to appear in dir, then watches it. It is
// meant to be launched as `go watch.RunBackground(ctx, dir)` alongside another
// UI (TUI/REPL). The snapshot is reset so each background session re-anchors to
// the file's current content rather than replaying an earlier session's state.
func RunBackground(ctx context.Context, dir string) {
	notebookPath := filepath.Join(dir, DefaultNotebook)
	if !waitForFile(ctx, notebookPath) {
		return // ctx cancelled before the file appeared
	}
	clearSnapshot(dir)

	briefer, _, _ := NewAgentBriefer("code-mimi", dir)
	_ = Run(ctx, Config{
		Dir:          dir,
		NotebookPath: notebookPath,
		Brief:        briefer,    // nil → defaultBrief
		Out:          io.Discard, // silent — all activity is visible in the file
	})
}

// Run sets up the folder and watches the notebook until ctx is cancelled.
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
	// Show the engineer, on top, that the file is being tracked.
	stampHeaderIfEmpty(cfg.NotebookPath)
	ensureGitignored(cfg.Dir, snapshotFileName)

	// Anchor to the file's current content the first time we run, so existing
	// text (including the header we just stamped) is never treated as new input.
	if !snapshotExists(cfg.Dir) {
		if data, err := os.ReadFile(cfg.NotebookPath); err == nil {
			writeSnapshot(cfg.Dir, normalizeLF(string(data)))
		}
	}

	fmt.Fprintf(cfg.Out, "[mimi] tracking %s\n", cfg.NotebookPath)
	fmt.Fprintln(cfg.Out, "[mimi] write below and save to send")

	ticker := time.NewTicker(cfg.Poll)
	defer ticker.Stop()

	// settled holds the last content we saw on a previous tick. We only act
	// once a change has been stable across two ticks — that debounces rapid
	// saves and avoids reacting to a half-flushed file mid-save.
	var settled string

	for {
		select {
		case <-ctx.Done():
			fmt.Fprintln(cfg.Out, "[mimi] stopped")
			return nil

		case <-ticker.C:
			raw, err := os.ReadFile(cfg.NotebookPath)
			if err != nil {
				continue // file briefly missing (e.g. atomic-save rename); retry
			}
			current := normalizeLF(string(raw))
			snapshot := readSnapshot(cfg.Dir)

			if current == snapshot {
				settled = current
				continue
			}
			if current != settled {
				settled = current // not stable yet; wait one more tick
				continue
			}

			delta := extractNewContent(snapshot, current)
			if strings.TrimSpace(delta) == "" {
				// Only blank lines / whitespace changed — absorb without a turn.
				writeSnapshot(cfg.Dir, current)
				continue
			}

			fmt.Fprintf(cfg.Out, "[mimi] input detected (%d bytes), thinking…\n", len(delta))

			response, briefErr := cfg.Brief(ctx, cfg.Dir, delta)
			if briefErr != nil {
				fmt.Fprintf(cfg.Out, "[mimi] error: %v\n", briefErr)
				suffix, _ := appendResponse(cfg.NotebookPath, fmt.Sprintf("[mimi error: %v]", briefErr))
				writeSnapshot(cfg.Dir, normalizeLF(current+suffix))
				continue
			}
			if strings.TrimSpace(response) == "" {
				// Nothing to say, but mark the input seen so we don't re-ask.
				writeSnapshot(cfg.Dir, current)
				continue
			}

			suffix, err := appendResponse(cfg.NotebookPath, response)
			if err != nil {
				fmt.Fprintf(cfg.Out, "[mimi] append failed: %v\n", err)
				continue // leave snapshot untouched; retry next tick
			}
			// Extend the snapshot in memory rather than re-reading: anything the
			// engineer typed while mimi was thinking is intentionally left out of
			// the snapshot, so it gets picked up on the next pass instead of lost.
			writeSnapshot(cfg.Dir, normalizeLF(current+suffix))

			fmt.Fprintf(cfg.Out, "[mimi] answered (%d bytes)\n", len(response))
		}
	}
}

// ── helpers ───────────────────────────────────────────────────────────────

// waitForFile blocks until path exists or ctx is cancelled. Returns true if
// the file appeared, false if cancelled first.
func waitForFile(ctx context.Context, path string) bool {
	if _, err := os.Stat(path); err == nil {
		return true
	}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
			if _, err := os.Stat(path); err == nil {
				return true
			}
		}
	}
}

func withDefaults(cfg Config) Config {
	if cfg.NotebookPath == "" {
		cfg.NotebookPath = filepath.Join(cfg.Dir, DefaultNotebook)
	}
	if cfg.Poll <= 0 {
		cfg.Poll = 500 * time.Millisecond
	}
	if cfg.Brief == nil {
		cfg.Brief = defaultBrief
	}
	if cfg.Out == nil {
		cfg.Out = os.Stderr
	}
	return cfg
}

// ensureGitignored appends entry to .gitignore if absent (best-effort).
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

// defaultBrief is the no-LLM fallback used when no Briefer is configured (e.g.
// the API key is missing). It keeps the loop functional instead of panicking.
func defaultBrief(_ context.Context, _, _ string) (string, error) {
	return "(no agent active — set ANTHROPIC_API_KEY and restart for real answers)", nil
}
