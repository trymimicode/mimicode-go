// Package watch starts mimi in a folder as an ambient assistant: it watches a
// plain-text notebook the engineer thinks out loud in, and on each change asks a
// Briefer for material to learn from, then hands that to an Injector to surface.
//
// mimi never writes the engineer's code. It gathers and corrects understanding;
// the engineer keeps the whole program in their head. The intelligence (the
// Briefer that corrects with cited evidence + real git source) and the surface
// (the Injector) are seams — defaults here are deliberately minimal so the
// watcher+inject loop can be owned separately.
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

// Briefer turns the engineer's current out-loud thinking into material to learn
// from: corrections, citations, real source pointers. notebook is the full
// current contents of the notebook (the engineer's portion only — the injected
// brief is stripped before this is called).
type Briefer func(ctx context.Context, dir, notebook string) (string, error)

// Injector surfaces a brief to the engineer. The default appends it under a
// marker inside the notebook; a richer implementation might use a side pane or
// a separate file. It must not disturb the engineer's own text.
type Injector func(notebookPath, brief string) error

// Config configures a watch session. Dir is required; everything else has a
// sensible default.
type Config struct {
	Dir          string
	NotebookPath string        // default: <Dir>/code.mimi
	Poll         time.Duration // default: 700ms
	Brief        Briefer       // default: defaultBrief (local gather, no LLM)
	Inject       Injector      // default: appendBrief
	Out          io.Writer     // status messages; default: os.Stderr
}

// BriefMarker delimits mimi's injected brief from the engineer's own writing.
const BriefMarker = "=== mimi ==="

// Run sets up the folder (cache dir, repomap, notebook) and watches the
// notebook until ctx is cancelled. It is dependency-free (mtime polling) so the
// watcher mechanism can be replaced without pulling in new deps.
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

	fmt.Fprintf(cfg.Out, "[mimi] watching %s\n", cfg.Dir)
	fmt.Fprintf(cfg.Out, "[mimi] think out loud in %s — mimi gathers, you write the code\n", cfg.NotebookPath)

	ticker := time.NewTicker(cfg.Poll)
	defer ticker.Stop()

	var lastSeen string
	for {
		select {
		case <-ctx.Done():
			fmt.Fprintln(cfg.Out, "\n[mimi] stopped watching")
			return nil
		case <-ticker.C:
			thinking, ok := readThinking(cfg.NotebookPath)
			if !ok || strings.TrimSpace(thinking) == "" || thinking == lastSeen {
				continue
			}
			lastSeen = thinking

			brief, err := cfg.Brief(ctx, cfg.Dir, thinking)
			if err != nil {
				fmt.Fprintf(cfg.Out, "[mimi] brief error: %v\n", err)
				continue
			}
			if strings.TrimSpace(brief) == "" {
				continue
			}
			if err := cfg.Inject(cfg.NotebookPath, brief); err != nil {
				fmt.Fprintf(cfg.Out, "[mimi] inject error: %v\n", err)
			}
		}
	}
}

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
	if cfg.Inject == nil {
		cfg.Inject = appendBrief
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
	header := "# think out loud here — half-formed ideas welcome.\n" +
		"# mimi reads this, corrects your understanding with real evidence,\n" +
		"# and fetches real source to learn from. you write the code.\n\n"
	return os.WriteFile(path, []byte(header), 0o644)
}

// readThinking returns the engineer's portion of the notebook (everything before
// the first injected brief marker), so a Briefer never reacts to its own output.
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

// defaultBrief is a local, no-LLM gather: it reflects the codebase shape back so
// `mimi watch` does something useful out of the box. The real correct-with-
// evidence loop (LLM + web + git_source) is meant to replace this via Config.Brief.
func defaultBrief(ctx context.Context, dir, notebook string) (string, error) {
	var b strings.Builder
	b.WriteString("(local gather — wire Config.Brief for the LLM correct-loop)\n")

	if rm := repomap.Cached(); rm != "" {
		if len(rm) > 1200 {
			rm = rm[:1200] + "\n... (truncated)"
		}
		b.WriteString("\nrepo shape:\n")
		b.WriteString(rm)
	}
	if repos := gitsource.List(dir); len(repos) > 0 {
		b.WriteString("\n\ncached source to read:")
		for _, r := range repos {
			fmt.Fprintf(&b, "\n  %s", r.LocalPath)
		}
	}
	return b.String(), nil
}
