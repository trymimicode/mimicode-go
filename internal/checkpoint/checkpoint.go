// Package checkpoint snapshots the project working tree into a shadow git repo
// after each agent turn, so changes are reversible with :undo. The shadow repo
// lives in the session dir and never touches the user's real .git history.
package checkpoint

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

type Checkpointer struct {
	gitDir   string
	workTree string
	enabled  bool
}

type Entry struct {
	SHA   string
	Label string
}

// New initializes a shadow git repo at <sessionDir>/checkpoints.git with the
// project cwd as its work tree. Best-effort: if git is missing or disabled via
// MIMICODE_CHECKPOINT=0, returns a disabled Checkpointer that no-ops.
func New(sessionDir, cwd string) *Checkpointer {
	if os.Getenv("MIMICODE_CHECKPOINT") == "0" {
		return &Checkpointer{enabled: false}
	}
	if _, err := exec.LookPath("git"); err != nil {
		fmt.Fprintln(os.Stderr, "checkpoint: git not found; :undo disabled")
		return &Checkpointer{enabled: false}
	}
	c := &Checkpointer{
		gitDir:   filepath.Join(sessionDir, "checkpoints.git"),
		workTree: cwd,
		enabled:  true,
	}
	if _, err := os.Stat(c.gitDir); os.IsNotExist(err) {
		if _, err := c.git("init", "--quiet"); err != nil {
			fmt.Fprintf(os.Stderr, "checkpoint: init failed; :undo disabled: %v\n", err)
			c.enabled = false
			return c
		}
		// Never snapshot the user's real repo metadata or our own session data.
		excludePath := filepath.Join(c.gitDir, "info", "exclude")
		_ = os.MkdirAll(filepath.Dir(excludePath), 0o755)
		_ = os.WriteFile(excludePath, []byte("/.git/\n/.mimi/\n"), 0o644)
	}
	return c
}

func (c *Checkpointer) Enabled() bool { return c != nil && c.enabled }

// Snapshot stages the whole work tree and commits it with label.
// Returns the short SHA, or "" if there was nothing to commit.
func (c *Checkpointer) Snapshot(label string) (string, error) {
	if !c.Enabled() {
		return "", nil
	}
	if _, err := c.git("add", "-A"); err != nil {
		return "", fmt.Errorf("checkpoint: stage: %w", err)
	}
	out, err := c.git(
		"-c", "user.email=mimi@localhost",
		"-c", "user.name=mimicode",
		"-c", "commit.gpgsign=false",
		"commit", "--quiet", "-m", label,
	)
	if err != nil {
		if strings.Contains(out, "nothing to commit") || strings.Contains(out, "no changes added") {
			return "", nil
		}
		return "", fmt.Errorf("checkpoint: commit: %s", strings.TrimSpace(out))
	}
	sha, _ := c.git("rev-parse", "--short", "HEAD")
	return strings.TrimSpace(sha), nil
}

// Undo restores the work tree to the state n checkpoints back (default 1).
// It will not go past the earliest (baseline) checkpoint. Returns the label of
// the checkpoint restored to. Reset commits remain recoverable via git reflog.
func (c *Checkpointer) Undo(n int) (string, error) {
	if !c.Enabled() {
		return "", fmt.Errorf("checkpointing is disabled")
	}
	if n <= 0 {
		n = 1
	}
	count := c.commitCount()
	if count == 0 {
		return "", fmt.Errorf("no checkpoints yet")
	}
	if n > count-1 {
		n = count - 1
	}
	if n == 0 {
		return "", fmt.Errorf("already at the earliest checkpoint")
	}
	target := fmt.Sprintf("HEAD~%d", n)
	label, _ := c.git("log", "-1", "--format=%s", target)
	if _, err := c.git("reset", "--hard", "--quiet", target); err != nil {
		return "", fmt.Errorf("checkpoint: undo: %w", err)
	}
	return strings.TrimSpace(label), nil
}

// List returns checkpoints newest-first.
func (c *Checkpointer) List() []Entry {
	if !c.Enabled() {
		return nil
	}
	out, err := c.git("log", "--format=%h\t%s")
	if err != nil {
		return nil
	}
	var entries []Entry
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) == 2 {
			entries = append(entries, Entry{SHA: parts[0], Label: parts[1]})
		}
	}
	return entries
}

func (c *Checkpointer) commitCount() int {
	out, err := c.git("rev-list", "--count", "HEAD")
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(out))
	return n
}

func (c *Checkpointer) git(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = c.workTree
	cmd.Env = append(os.Environ(),
		"GIT_DIR="+c.gitDir,
		"GIT_WORK_TREE="+c.workTree,
	)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
