package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func mimiDir(cwd string) string {
	return filepath.Join(cwd, ".mimi")
}

// LoadMemory returns the contents of .mimi/MEMORY.md, or "" if absent.
func LoadMemory(cwd string) string {
	return readFile(filepath.Join(mimiDir(cwd), "MEMORY.md"))
}

// LoadRules returns the contents of .mimi/RULES.md, or "" if absent.
func LoadRules(cwd string) string {
	return readFile(filepath.Join(mimiDir(cwd), "RULES.md"))
}

// AppendRule appends a single behavioral rule to .mimi/RULES.md, dated.
// Used by the self-recovery loop to record a lesson learned from a failure.
func AppendRule(cwd, rule string) error {
	rule = strings.TrimSpace(rule)
	if rule == "" {
		return fmt.Errorf("empty rule")
	}
	dir := mimiDir(cwd)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, "RULES.md")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "- %s _(learned %s)_\n", rule, time.Now().UTC().Format("2006-01-02"))
	return err
}

func readFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

// HandleMemoryWrite appends a markdown section to .mimi/MEMORY.md.
// Required args: component (string), summary (string).
// Optional: detail, related_files, tags, open_issues, change_entry.
func HandleMemoryWrite(sessionID string, args map[string]any, cwd string) string {
	component, ok := stringArg(args, "component")
	if !ok || component == "" {
		return "memory write error: missing required arg 'component'"
	}
	summary, ok := stringArg(args, "summary")
	if !ok || summary == "" {
		return "memory write error: missing required arg 'summary'"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "## %s — %s\n", component, time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "**Summary:** %s\n", summary)

	if detail, ok := stringArg(args, "detail"); ok && detail != "" {
		fmt.Fprintf(&b, "%s\n", detail)
	}

	if files := stringSliceArg(args, "related_files"); len(files) > 0 {
		fmt.Fprintf(&b, "**Files:** %s\n", strings.Join(files, ", "))
	}

	if tags := stringSliceArg(args, "tags"); len(tags) > 0 {
		fmt.Fprintf(&b, "**Tags:** %s\n", strings.Join(tags, ", "))
	}

	if ce, ok := args["change_entry"].(map[string]any); ok {
		file, _ := ce["file"].(string)
		what, _ := ce["what"].(string)
		why, _ := ce["why"].(string)
		if file != "" || what != "" {
			fmt.Fprintf(&b, "**Change:** %s: %s (%s)\n", file, what, why)
		}
	}

	if issues := stringSliceArg(args, "open_issues"); len(issues) > 0 {
		b.WriteString("**Open issues:**\n")
		for _, issue := range issues {
			fmt.Fprintf(&b, "- %s\n", issue)
		}
	}

	b.WriteString("\n")

	dir := mimiDir(cwd)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Sprintf("memory write error: mkdir: %v", err)
	}

	path := filepath.Join(dir, "MEMORY.md")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Sprintf("memory write error: open: %v", err)
	}
	defer f.Close()

	if _, err := f.WriteString(b.String()); err != nil {
		return fmt.Sprintf("memory write error: write: %v", err)
	}

	return "memory written: " + component
}

func stringArg(args map[string]any, key string) (string, bool) {
	v, ok := args[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

func stringSliceArg(args map[string]any, key string) []string {
	v, ok := args[key]
	if !ok {
		return nil
	}
	switch typed := v.(type) {
	case []string:
		return typed
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
