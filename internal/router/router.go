package router

import "strings"

const (
	HAIKU  = "claude-haiku-4-5-20251001"
	SONNET = "claude-sonnet-4-5-20250929"
)

// ModelChoice is the routing result for a single user turn.
type ModelChoice struct {
	Model    string
	Reason   string
	Guidance string
}

// ParseIntent routes userText to a model based on keyword matching.
// SONNET-specific conditions are evaluated first; HAIKU conditions follow.
// The fall-through default is SONNET with reason "default".
func ParseIntent(userText string) ModelChoice {
	t := strings.ToLower(userText)

	// ── SONNET: planning ──────────────────────────────────────────────────────
	for _, kw := range []string{
		"architecture", "design pattern", "best approach", "should i",
		"strategy", "how to structure", "overall plan",
	} {
		if strings.Contains(t, kw) {
			return ModelChoice{Model: SONNET, Reason: "planning"}
		}
	}

	// ── SONNET: multi_file ────────────────────────────────────────────────────
	for _, kw := range []string{
		"all files", "every file", "across files", "multiple files",
		"entire codebase", "project-wide", "refactor all", "rename everywhere",
	} {
		if strings.Contains(t, kw) {
			return ModelChoice{Model: SONNET, Reason: "multi_file"}
		}
	}

	// ── SONNET: debugging ─────────────────────────────────────────────────────
	for _, kw := range []string{
		"not working", "doesn't work", "does not work", "broken",
		"bug", "debug",
		"why does", "why is", "why isn't", "why doesn't",
		"error", "fail", "crash", "stall", "stuck", "wrong",
		"issue", "problem", "investigate", "diagnose",
	} {
		if strings.Contains(t, kw) {
			return ModelChoice{Model: SONNET, Reason: "debugging"}
		}
	}

	// ── HAIKU: simple_bash ────────────────────────────────────────────────────
	for _, kw := range []string{"run", "execute", "pytest", "python"} {
		if strings.Contains(t, kw) {
			return ModelChoice{
				Model:    HAIKU,
				Reason:   "simple_bash",
				Guidance: "Execute commands directly. Show output clearly.",
			}
		}
	}

	// ── HAIKU: simple_search ──────────────────────────────────────────────────
	for _, kw := range []string{
		"find", "search", "where", "show me", "list", "grep", "look for",
	} {
		if strings.Contains(t, kw) {
			return ModelChoice{
				Model:    HAIKU,
				Reason:   "simple_search",
				Guidance: "Use `rg` for all searches. Be precise with file:line citations.",
			}
		}
	}

	// ── HAIKU: simple_read ────────────────────────────────────────────────────
	for _, kw := range []string{
		"read", "check", "what does", "what is", "how does",
	} {
		if strings.Contains(t, kw) {
			return ModelChoice{
				Model:    HAIKU,
				Reason:   "simple_read",
				Guidance: "Read files systematically. Quote relevant sections.",
			}
		}
	}

	// ── HAIKU: simple_edit (compound: verb AND file indicator) ────────────────
	editVerbs := []string{"change", "fix", "update", "modify", "edit", "replace"}
	fileIndicators := []string{
		".py", ".js", ".ts", ".go", ".java", ".rb", ".md", ".txt",
		"in file", "in the file", "single file", "one file", "this file",
	}

	hasVerb := false
	for _, v := range editVerbs {
		if strings.Contains(t, v) {
			hasVerb = true
			break
		}
	}
	if hasVerb {
		for _, f := range fileIndicators {
			if strings.Contains(t, f) {
				return ModelChoice{
					Model:    HAIKU,
					Reason:   "simple_edit",
					Guidance: "Read before editing. Use exact old_text with 2-3 lines context.",
				}
			}
		}
	}

	// ── default ───────────────────────────────────────────────────────────────
	return ModelChoice{Model: SONNET, Reason: "default"}
}

// RouteTurn is the public entry point used by the agent.
func RouteTurn(userText string) ModelChoice {
	return ParseIntent(userText)
}

// AugmentSystemPrompt appends task-specific guidance to the base system prompt.
// If guidance is empty, base is returned unchanged.
func AugmentSystemPrompt(base, guidance string) string {
	if guidance == "" {
		return base
	}
	return base + "\n\n**TASK GUIDANCE:**\n" + guidance
}
