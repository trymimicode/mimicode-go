package tools

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// DiffInfo describes a file change produced by Write or Edit.
type DiffInfo struct {
	Path       string
	OldContent string
	NewContent string
	Operation  string // "write" | "edit"
	IsNewFile  bool
}

// ToolResult is the uniform return type for all tool functions.
type ToolResult struct {
	Output    string
	IsError   bool
	Truncated bool
	TimedOut  bool
	DiffInfo  *DiffInfo
}

// EditOp is a single find-and-replace pair for Edit.
type EditOp struct {
	OldText string
	NewText string
}

// fileLocks serialises concurrent writes to the same absolute path.
var fileLocks sync.Map

func lockForPath(absPath string) *sync.Mutex {
	v, _ := fileLocks.LoadOrStore(absPath, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// ── Bash pre-flight vetting ───────────────────────────────────────────────────

// cmdPos matches command position: start of string or after a pipeline/chain operator.
const cmdPos = `(?:^|[|&;])\s*`

type vetRule struct {
	re   *regexp.Regexp
	hint string
}

var bashVetRules = []vetRule{
	{
		regexp.MustCompile(cmdPos + `find\s`),
		`"find" is blocked at command position; use "rg --files" instead`,
	},
	{
		regexp.MustCompile(cmdPos + `grep\s+-[rR]`),
		`"grep -r/-R" is blocked; use "rg" instead`,
	},
	{
		regexp.MustCompile(cmdPos + `ls\s+-R`),
		`"ls -R" is blocked; use "rg --files" instead`,
	},
	{
		regexp.MustCompile(cmdPos + `cat\s+\S+\.(py|js|ts|go|rs|rb|java|c|cpp|h|md|json|yaml|yml|toml)\b`),
		`"cat" on code/config files is blocked; use the Read tool instead`,
	},
	{
		regexp.MustCompile(`curl\b[^|]*\|\s*(ba)?sh\b`),
		`"curl | sh/bash" is blocked; inspect the script before executing`,
	},
	{
		regexp.MustCompile(cmdPos + `rm\s+-rf\s+[/~*]`),
		`"rm -rf /", "rm -rf ~", and "rm -rf *" are blocked`,
	},
}

// ── ANSI stripping & output truncation ───────────────────────────────────────

var (
	reANSICSI = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`)
	reANSIOSC = regexp.MustCompile(`\x1b\][^\x07]*\x07`)
)

func stripANSI(s string) string {
	s = reANSICSI.ReplaceAllString(s, "")
	return reANSIOSC.ReplaceAllString(s, "")
}

const maxOutputBytes = 100_000

func tailTruncate(b []byte) (string, bool) {
	if len(b) <= maxOutputBytes {
		return string(b), false
	}
	dropped := len(b) - maxOutputBytes
	header := fmt.Sprintf("[... truncated %d bytes; showing last %d ...]\n", dropped, maxOutputBytes)
	return header + string(b[dropped:]), true
}

// ── Bash ─────────────────────────────────────────────────────────────────────

// shell returns the shell binary and flag appropriate for the OS.
func shell() (string, string) {
	if sh, err := exec.LookPath("bash"); err == nil {
		return sh, "-c"
	}
	// Windows fallback: try PowerShell then cmd.
	if ps, err := exec.LookPath("powershell"); err == nil {
		return ps, "-Command"
	}
	return "cmd", "/C"
}

// Bash executes cmd in a shell with the given working directory.
// timeout is in seconds; 0 means no timeout.
func Bash(ctx context.Context, cwd, cmd string, timeout float64) ToolResult {
	for _, rule := range bashVetRules {
		if rule.re.MatchString(cmd) {
			return ToolResult{Output: rule.hint, IsError: true}
		}
	}

	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(float64(time.Second)*timeout))
		defer cancel()
	}

	sh, flag := shell()
	c := exec.CommandContext(ctx, sh, flag, cmd)
	c.Dir = cwd
	raw, err := c.CombinedOutput()

	output, truncated := tailTruncate([]byte(stripANSI(string(raw))))
	result := ToolResult{Output: output, Truncated: truncated}

	if err != nil {
		result.IsError = true
		switch ctx.Err() {
		case context.DeadlineExceeded:
			result.TimedOut = true
			if result.Output == "" {
				result.Output = fmt.Sprintf("[timeout after %.0fs]", timeout)
			}
		case context.Canceled:
			if result.Output == "" {
				result.Output = "[context cancelled]"
			}
		default:
			if exitErr, ok := err.(*exec.ExitError); ok {
				if result.Output == "" {
					result.Output = fmt.Sprintf("[exit %d, no output]", exitErr.ExitCode())
				}
			} else if result.Output == "" {
				result.Output = fmt.Sprintf("[error: %v]", err)
			}
		}
	}

	return result
}

// ── helpers ───────────────────────────────────────────────────────────────────

func resolvePath(cwd, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(cwd, path)
}

func isBinary(data []byte) bool {
	n := len(data)
	if n > 8192 {
		n = 8192
	}
	return bytes.IndexByte(data[:n], 0) >= 0
}

// ── Read ──────────────────────────────────────────────────────────────────────

// Read returns a line-numbered window of a text file.
// offset is 1-indexed; default is 1. limit defaults to 2000.
func Read(ctx context.Context, cwd, path string, offset, limit int) ToolResult {
	absPath := resolvePath(cwd, path)

	info, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return ToolResult{Output: fmt.Sprintf("file not found: %s", absPath), IsError: true}
		}
		return ToolResult{Output: fmt.Sprintf("stat: %v", err), IsError: true}
	}
	if info.IsDir() {
		return ToolResult{Output: fmt.Sprintf("%s is a directory", absPath), IsError: true}
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		return ToolResult{Output: fmt.Sprintf("read: %v", err), IsError: true}
	}
	if isBinary(data) {
		return ToolResult{Output: "binary file", IsError: true}
	}

	if offset <= 0 {
		offset = 1
	}
	if limit <= 0 {
		limit = 2000
	}

	lines := strings.Split(string(data), "\n")
	total := len(lines)

	start := offset - 1
	if start >= total {
		return ToolResult{
			Output:  fmt.Sprintf("offset %d exceeds file length (%d lines)", offset, total),
			IsError: true,
		}
	}

	end := start + limit
	if end > total {
		end = total
	}

	var sb strings.Builder
	for i := start; i < end; i++ {
		fmt.Fprintf(&sb, "%4d|%s\n", i+1, lines[i])
	}
	if end < total {
		fmt.Fprintf(&sb, "[... showing lines %d-%d of %d; use offset/limit for more]", offset, end, total)
	}

	return ToolResult{Output: sb.String()}
}

// ── Write ─────────────────────────────────────────────────────────────────────

// Write creates or overwrites a file with the given content.
// Concurrent writes to the same path are serialised.
func Write(ctx context.Context, cwd, path, content string) ToolResult {
	absPath := resolvePath(cwd, path)

	mu := lockForPath(absPath)
	mu.Lock()
	defer mu.Unlock()

	var oldContent string
	isNewFile := true
	if existing, err := os.ReadFile(absPath); err == nil {
		oldContent = string(existing)
		isNewFile = false
	}

	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return ToolResult{Output: fmt.Sprintf("mkdir: %v", err), IsError: true}
	}
	if err := os.WriteFile(absPath, []byte(content), 0o644); err != nil {
		return ToolResult{Output: fmt.Sprintf("write: %v", err), IsError: true}
	}

	return ToolResult{
		Output: fmt.Sprintf("wrote %d bytes to %s", len(content), absPath),
		DiffInfo: &DiffInfo{
			Path:       absPath,
			OldContent: oldContent,
			NewContent: content,
			Operation:  "write",
			IsNewFile:  isNewFile,
		},
	}
}

// ── Edit ──────────────────────────────────────────────────────────────────────

// Edit applies find-and-replace edits to a file.
// Provide either oldText+newText (single edit) or edits[] (batch), not both.
// All edits are validated and applied in memory; the file is only written if
// every edit succeeds (atomic).
func Edit(ctx context.Context, cwd, path string, oldText, newText string, edits []EditOp) ToolResult {
	hasSingle := oldText != "" || newText != ""
	hasEdits := len(edits) > 0

	switch {
	case hasSingle && hasEdits:
		return ToolResult{Output: "cannot specify both single edit (oldText/newText) and edits[]", IsError: true}
	case !hasSingle && !hasEdits:
		return ToolResult{Output: "must specify either single edit (oldText/newText) or edits[]", IsError: true}
	case hasSingle:
		edits = []EditOp{{OldText: oldText, NewText: newText}}
	}

	for i, op := range edits {
		if op.OldText == "" || op.NewText == "" {
			return ToolResult{Output: fmt.Sprintf("edit[%d]: both OldText and NewText are required", i), IsError: true}
		}
		if op.OldText == op.NewText {
			return ToolResult{Output: fmt.Sprintf("edit[%d]: OldText and NewText are identical", i), IsError: true}
		}
	}

	absPath := resolvePath(cwd, path)

	mu := lockForPath(absPath)
	mu.Lock()
	defer mu.Unlock()

	data, err := os.ReadFile(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return ToolResult{Output: fmt.Sprintf("file not found: %s", path), IsError: true}
		}
		return ToolResult{Output: fmt.Sprintf("read: %v", err), IsError: true}
	}
	if isBinary(data) {
		return ToolResult{Output: "binary file", IsError: true}
	}

	oldContent := string(data)
	buf := oldContent
	appliedLines := make([]int, 0, len(edits))

	for i, op := range edits {
		count := strings.Count(buf, op.OldText)
		switch count {
		case 0:
			return ToolResult{
				Output:  fmt.Sprintf("edit[%d]: OldText not found (%d prior edit(s) applied); verify the text matches exactly", i, i),
				IsError: true,
			}
		case 1:
			idx := strings.Index(buf, op.OldText)
			line := strings.Count(buf[:idx], "\n") + 1
			appliedLines = append(appliedLines, line)
			buf = buf[:idx] + op.NewText + buf[idx+len(op.OldText):]
		default:
			return ToolResult{
				Output:  fmt.Sprintf("edit[%d]: OldText matches %d times; add more surrounding context to make it unique", i, count),
				IsError: true,
			}
		}
	}

	if err := os.WriteFile(absPath, []byte(buf), 0o644); err != nil {
		return ToolResult{Output: fmt.Sprintf("write: %v", err), IsError: true}
	}

	lineStrs := make([]string, len(appliedLines))
	for i, l := range appliedLines {
		lineStrs[i] = fmt.Sprintf("%d", l)
	}

	return ToolResult{
		Output: fmt.Sprintf("applied %d edit(s) at line(s) %s", len(edits), strings.Join(lineStrs, ", ")),
		DiffInfo: &DiffInfo{
			Path:       absPath,
			OldContent: oldContent,
			NewContent: buf,
			Operation:  "edit",
			IsNewFile:  false,
		},
	}
}
