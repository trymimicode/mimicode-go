package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/trymimicode/mimicode-go/internal/compactor"
	"github.com/trymimicode/mimicode-go/internal/memory"
	"github.com/trymimicode/mimicode-go/internal/provider"
	"github.com/trymimicode/mimicode-go/internal/repomap"
	"github.com/trymimicode/mimicode-go/internal/store"
	"github.com/trymimicode/mimicode-go/internal/tools"
)

const SYSTEM_PROMPT = `You are mimicode — an intelligent rubber duck for engineers who want to stay sharp. You shall only be known as mimicode.

Your purpose is to help engineers develop their own judgment, not to replace it. You do not solve problems for them. You do not reach conclusions. You do not architect. You surface the right information at the right moment and ask the one question that helps them get there themselves.

TWO MODES. Nothing else exists:

1. RUBBER DUCK (default — triggered by any question, analysis, debugging, or architecture discussion):
   - Fetch and surface the relevant code, doc, or error. Point to the exact file:line.
   - Do not conclude. Do not pick the solution. Do not explain what they should do.
   - End with ONE question that nudges them one step forward. Make them think.
   - If they're stuck, surface a related resource or a simpler version of the problem.
   - The engineer must write the code. That's the whole point.

2. DETERMINISTIC EXECUTION (triggered only by explicit write instructions — "fix", "implement", "write", "add", "do it"):
   - Write exactly what was asked. No interpretation. No improvements. No extras.
   - Match the existing style character-for-character.
   - Smallest possible change. Verify it builds.

NEVER: conclude on architecture, pick between approaches, volunteer opinions, rewrite things that weren't broken, or remove the engineer's need to think.

Tools: read, bash, edit, write, memory_write, memory_search, web_search, web_fetch, stackoverflow_search, git_source.

- rg for search. read tool for files. Never cat, ls -R, grep -r, find.
- edit for partial changes (exact unique context). write for new files or full rewrites only.
- stackoverflow_search for errors. web_search/web_fetch for docs. git_source for real library source.
- After a meaningful change: memory_write. Prior work: memory_search first.

Output: minimal. No filler. No "Great question". Reference code as file:line. Don't paste diffs. Don't create .md summaries.`

// toolFormatGuide is appended to the persona for non-Claude providers (Kimi,
// MiniMax, GLM, etc.). These models are far more sensitive to tool-call shape
// than Claude — they routinely stringify array arguments or describe a call in
// prose instead of emitting it. Spelling out the exact format with one concrete
// example per mutating tool measurably improves compliance. Claude does not need
// this, so it only ships in the compat persona.
const toolFormatGuide = `## Tool-call format — follow exactly
Emit every tool call as a structured tool_use. Never describe a call in prose, never wrap arguments in markdown, never send arguments as a quoted/stringified blob — they must be a real JSON object.

- edit — "edits" is a JSON ARRAY of objects, not a string:
  {"path":"main.go","edits":[{"old_text":"foo()","new_text":"bar()"}]}
- write — full file content in "content":
  {"path":"notes.md","content":"line one\nline two\n"}
- bash — one command in "cmd":
  {"cmd":"go test ./..."}

Make one tool call at a time unless the calls are independent. After an edit or write, verify with bash. Never repeat a call that already succeeded.`

// SYSTEM_PROMPT_COMPAT is the persona for weaker, format-sensitive providers. It
// keeps the identical rubber-duck persona and only adds explicit tool-call
// formatting guidance — the product behavior is unchanged, the model just gets
// the extra scaffolding it needs to call tools correctly.
const SYSTEM_PROMPT_COMPAT = SYSTEM_PROMPT + "\n\n" + toolFormatGuide

type AgentConfig struct {
	CWD      string
	MaxSteps int
	Session  *store.Session // nil = no logging
	StreamCB provider.StreamCallback
	Model    string            // empty = use Provider.DefaultModel()
	Provider provider.Provider // nil = provider.Claude
	// ConfirmTool, if set, is called before each mutating tool (bash/write/edit).
	// Returning false blocks the call. nil = no gating.
	ConfirmTool func(name string, input map[string]any) bool
}

// gatedTools are the side-effecting tools the confirm-gate guards.
var gatedTools = map[string]bool{"bash": true, "write": true, "edit": true}

type AgentInterrupted struct{}

func (AgentInterrupted) Error() string { return "agent interrupted" }

// AgentStuck signals the loop detected a failure pattern (repeated identical
// tool calls, a run of tool errors, or exhausting the step budget) and gave up
// so the caller can run a clean-context recovery diagnosis.
type AgentStuck struct{ Reason string }

func (s AgentStuck) Error() string { return "agent stuck: " + s.Reason }

const (
	repeatedCallLimit = 3 // same tool+input N times → stuck
	consecErrorLimit  = 4 // N tool errors in a row → stuck
)

var TOOLS = []provider.ToolSchema{
	{
		Name:        "bash",
		Description: "Run a shell command in the current working directory and return combined stdout/stderr plus exit status. Use it for builds, tests, linters, git, and 'rg' searches. A non-zero exit is reported, not hidden. Output is capped at ~100KB (last bytes kept); narrow the command if you hit the cap. Prefer one targeted command over a broad one.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"cmd":     map[string]any{"type": "string", "description": "Command to execute."},
				"timeout": map[string]any{"type": "number", "description": "Optional timeout in seconds."},
			},
			"required": []any{"cmd"},
		},
	},
	{
		Name:        "read",
		Description: "Read a text file with line numbers. Use offset and limit for large files.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":   map[string]any{"type": "string"},
				"offset": map[string]any{"type": "integer"},
				"limit":  map[string]any{"type": "integer"},
			},
			"required": []any{"path"},
		},
	},
	{
		Name:        "write",
		Description: "Create or overwrite a file with the provided content.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":    map[string]any{"type": "string"},
				"content": map[string]any{"type": "string"},
			},
			"required": []any{"path", "content"},
		},
	},
	{
		Name:        "edit",
		Description: "Edit an existing file by exact text replacement. Each old_text must match exactly once in the current file — include just enough surrounding context to be unique, and no more (do not pad with large unchanged regions). To change several places in the same file, pass multiple entries in 'edits'; they are matched against the original file and applied atomically (all-or-nothing) and must not overlap. Read the file before editing. For new files or a full rewrite, use 'write' instead.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":     map[string]any{"type": "string"},
				"old_text": map[string]any{"type": "string"},
				"new_text": map[string]any{"type": "string"},
				"edits": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"old_text": map[string]any{"type": "string"},
							"new_text": map[string]any{"type": "string"},
						},
						"required": []any{"old_text", "new_text"},
					},
				},
			},
			"required": []any{"path"},
		},
	},
	{
		Name:        "memory_write",
		Description: "Append a durable memory entry for future sessions.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"component":     map[string]any{"type": "string"},
				"summary":       map[string]any{"type": "string"},
				"detail":        map[string]any{"type": "string"},
				"related_files": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"tags":          map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"open_issues":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"change_entry":  map[string]any{"type": "object"},
			},
			"required": []any{"component", "summary"},
		},
	},
	{
		Name:        "memory_search",
		Description: "Search past session transcripts, memory, and rules using lexical FTS search.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string"},
				"top_k": map[string]any{"type": "integer"},
				"kind":  map[string]any{"type": "string", "enum": []any{"session", "memory", "rules"}},
			},
			"required": []any{"query"},
		},
	},
	{
		Name:        "recall_compaction",
		Description: "List compaction summaries or load a specific compaction by id.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{"type": "string", "description": "Optional compaction id to load."},
			},
		},
	},
	{
		Name:        "web_search",
		Description: "Search the web via DuckDuckGo. Returns title+url+snippet per result. Add site: filters in the query to scope results (e.g. site:stackoverflow.com).",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query":       map[string]any{"type": "string", "description": "Search query."},
				"max_results": map[string]any{"type": "integer", "description": "Max results to return (default 8)."},
			},
			"required": []any{"query"},
		},
	},
	{
		Name:        "web_fetch",
		Description: "Fetch a URL and return its main text. Handles GitHub issues, Reddit posts, HN threads, and Stack Overflow questions with dedicated extractors; falls back to generic HTML for everything else.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url": map[string]any{"type": "string", "description": "URL to fetch."},
			},
			"required": []any{"url"},
		},
	},
	{
		Name:        "stackoverflow_search",
		Description: "Search Stack Overflow and return matching questions with their top answers inline. Use for debugging errors and finding usage examples.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query":       map[string]any{"type": "string", "description": "Search query."},
				"max_results": map[string]any{"type": "integer", "description": "Max questions to return (default 3)."},
			},
			"required": []any{"query"},
		},
	},
	{
		Name:        "git_source",
		Description: "Shallow-clone a real repository into the project cache (.mimi/cache) and return its local path plus a file listing, so you can rg/read the actual source. Use to learn how a library really works instead of guessing.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"repo": map[string]any{"type": "string", "description": "Repo reference: \"owner/repo\" (GitHub), a host path, a full git URL, or scp form."},
				"ref":  map[string]any{"type": "string", "description": "Optional branch or tag to clone."},
			},
			"required": []any{"repo"},
		},
	},
}

// personaFor returns the static persona for a provider. The real Anthropic
// endpoint gets the lean prompt; every other backend (Kimi, MiniMax, GLM, …)
// gets the compat persona with explicit tool-call formatting. The result is a
// pure constant per provider, so it stays a stable, cacheable prefix.
func personaFor(p provider.Provider) string {
	if p == nil || p == provider.Claude {
		return SYSTEM_PROMPT
	}
	return SYSTEM_PROMPT_COMPAT
}

// BuildSystem assembles the system prompt as two parts joined by
// provider.SystemCacheBreak: the static persona (cacheable across every turn)
// and the volatile per-turn context (env, project instructions, repomap, rules,
// memory). The Claude builder turns the marker into separate cache breakpoints
// so a repomap refresh or a new rule no longer busts the persona cache.
func BuildSystem(cwd string, p provider.Provider) string {
	var b strings.Builder
	b.WriteString(personaFor(p))

	// Everything below is volatile and goes after the cache break.
	b.WriteString(provider.SystemCacheBreak)
	fmt.Fprintf(&b, "Current date: %s", time.Now().Format("2006-01-02"))
	fmt.Fprintf(&b, "\nCurrent working directory: %s", cwd)

	if path, content := loadProjectContext(cwd); content != "" {
		fmt.Fprintf(&b, "\n\n## Project instructions (%s)\n"+
			"These are the project's own conventions. Follow them.\n\n%s", path, content)
	}
	if repo := repomap.Cached(); repo != "" {
		fmt.Fprintf(&b, "\n\n## Repository map\n%s", repo)
	}
	if rules := memory.LoadAllRules(cwd); rules != "" {
		fmt.Fprintf(&b, "\n\n## Behavioral rules\n%s", rules)
	}
	if mem := memory.LoadMemory(cwd); mem != "" {
		fmt.Fprintf(&b, "\n\n## Memory\n%s", mem)
	}
	return b.String()
}

// projectContextFiles are the conventional names projects use to instruct coding
// agents, in priority order. The first one found wins.
var projectContextFiles = []string{"AGENTS.md", "CLAUDE.md", ".mimi/AGENTS.md"}

// loadProjectContext returns the path and contents of the project's own
// agent-instructions file (AGENTS.md / CLAUDE.md), if present. Oversized files
// are truncated so a stray doc can't blow the context budget.
func loadProjectContext(cwd string) (string, string) {
	const maxBytes = 32 * 1024
	for _, name := range projectContextFiles {
		path := filepath.Join(cwd, name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		content := strings.TrimSpace(string(data))
		if content == "" {
			continue
		}
		if len(content) > maxBytes {
			content = content[:maxBytes] + "\n…(truncated)"
		}
		return name, content
	}
	return "", ""
}

func AgentTurn(ctx context.Context, cfg AgentConfig, userMsg string, messages []provider.Message) ([]provider.Message, error) {
	cfg = normalizeConfig(cfg)

	var turn int
	if cfg.Session != nil {
		turn = cfg.Session.LogUser(userMsg)
	}

	messages = append(messages, provider.Message{
		Role:    "user",
		Content: []provider.ContentBlock{{Type: "text", Text: userMsg}},
	})

	system := BuildSystem(cfg.CWD, cfg.Provider)
	model := cfg.Model
	if model == "" {
		model = cfg.Provider.DefaultModel()
	}
	sessionDir := ""
	if cfg.Session != nil {
		sessionDir = cfg.Session.Path()
	}

	callCounts := map[string]int{}
	consecErrors := 0

	for step := 0; step < cfg.MaxSteps; step++ {
		if err := ctx.Err(); err != nil {
			return messages, AgentInterrupted{}
		}

		var record *compactor.CompactionRecord
		var err error
		if sessionDir != "" {
			messages, record, err = compactor.MaybeCompact(ctx, messages, sessionDir, provider.LastUsage().InputTokens)
			if err != nil {
				return messages, err
			}
			if record != nil && cfg.Session != nil {
				cfg.Session.LogCompaction(record.ID, record.Reason)
			}
		}

		t0 := time.Now()
		msg, usage, err := callModel(ctx, cfg, messages, system, model)
		ms := time.Since(t0).Milliseconds()
		if err != nil {
			return messages, err
		}
		messages = append(messages, msg)

		if cfg.Session != nil {
			cfg.Session.LogModel(turn, step+1, store.ModelEvent{
				Model:  model,
				Text:   msgText(msg),
				Calls:  msgCalls(msg),
				Tokens: store.TokenRec{In: usage.InputTokens, Out: usage.OutputTokens, CR: usage.CacheRead, CW: usage.CacheWrite},
				Ms:     ms,
			})
		}

		toolUses := toolUseBlocks(msg)
		if len(toolUses) == 0 {
			if cfg.Session != nil {
				cfg.Session.LogTurnEnd(turn, step+1, "no_tool_use")
			}
			repomap.RefreshAsync(cfg.CWD)
			return messages, nil
		}

		results := make([]provider.ContentBlock, 0, len(toolUses))
		stuck := ""
		for _, tu := range toolUses {
			if err := ctx.Err(); err != nil {
				return messages, AgentInterrupted{}
			}

			if cfg.ConfirmTool != nil && gatedTools[tu.Name] && !cfg.ConfirmTool(tu.Name, tu.Input) {
				if cfg.Session != nil {
					cfg.Session.LogToolExec(turn, step+1, store.ToolExecEvent{ID: tu.ID, Name: tu.Name, Input: tu.Input})
					cfg.Session.LogToolDone(turn, step+1, store.ToolDoneEvent{ID: tu.ID, Name: tu.Name, Error: true, Preview: "blocked by user"})
				}
				results = append(results, provider.ContentBlock{
					Type:      "tool_result",
					Content:   "Blocked: the user did not approve this " + tu.Name + " call. Do not retry it — choose a different approach or ask the user what they want.",
					IsError:   true,
					ToolUseID: tu.ID,
				})
				continue
			}

			if cfg.Session != nil {
				cfg.Session.LogToolExec(turn, step+1, store.ToolExecEvent{ID: tu.ID, Name: tu.Name, Input: tu.Input})
			}
			t1 := time.Now()
			result, diffInfo := dispatchTool(ctx, cfg, tu.Name, tu.Input)
			toolMs := time.Since(t1).Milliseconds()
			if cfg.StreamCB != nil && diffInfo != nil {
				cfg.StreamCB("file_change", map[string]any{
					"path":        diffInfo.Path,
					"old_content": diffInfo.OldContent,
					"new_content": diffInfo.NewContent,
					"operation":   diffInfo.Operation,
					"is_new_file": diffInfo.IsNewFile,
				})
			}
			result.ToolUseID = tu.ID
			if cfg.Session != nil {
				preview := result.Content
				if len(preview) > 300 {
					preview = preview[:300]
				}
				cfg.Session.LogToolDone(turn, step+1, store.ToolDoneEvent{
					ID:      tu.ID,
					Name:    tu.Name,
					Ms:      toolMs,
					Error:   result.IsError,
					Bytes:   len(result.Content),
					Preview: preview,
				})
			}
			results = append(results, result)

			sig := tu.Name + "|" + callSignature(tu.Input)
			callCounts[sig]++
			if callCounts[sig] >= repeatedCallLimit {
				stuck = fmt.Sprintf("repeated the same %s call %d times without progress", tu.Name, callCounts[sig])
			}
			if result.IsError {
				consecErrors++
			} else {
				consecErrors = 0
			}
			if consecErrors >= consecErrorLimit {
				stuck = fmt.Sprintf("%d consecutive tool errors", consecErrors)
			}
		}
		messages = append(messages, provider.Message{Role: "user", Content: results})

		if stuck != "" {
			if cfg.Session != nil {
				cfg.Session.LogTurnEnd(turn, step+1, "stuck:"+stuck)
			}
			repomap.RefreshAsync(cfg.CWD)
			return messages, AgentStuck{Reason: stuck}
		}
	}

	if cfg.Session != nil {
		cfg.Session.LogTurnEnd(turn, cfg.MaxSteps, "max_steps")
	}
	repomap.RefreshAsync(cfg.CWD)
	return messages, AgentStuck{Reason: fmt.Sprintf("hit the %d-step budget without finishing", cfg.MaxSteps)}
}

func callSignature(input map[string]any) string {
	b, err := json.Marshal(input)
	if err != nil {
		return fmt.Sprintf("%v", input)
	}
	return string(b)
}

func dispatchTool(ctx context.Context, cfg AgentConfig, name string, input map[string]any) (provider.ContentBlock, *tools.DiffInfo) {
	var output string
	var isErr bool
	var diffInfo *tools.DiffInfo

	switch name {
	case "bash":
		result := tools.Bash(ctx, cfg.CWD, stringInput(input, "cmd"), numberInput(input, "timeout"))
		output, isErr = result.Output, result.IsError
	case "read":
		result := tools.Read(ctx, cfg.CWD, stringInput(input, "path"), intInput(input, "offset"), intInput(input, "limit"))
		output, isErr = result.Output, result.IsError
		if cfg.StreamCB != nil && !isErr && result.Output != "" {
			cfg.StreamCB("file_read", map[string]any{
				"path":   stringInput(input, "path"),
				"output": result.Output,
			})
		}
	case "write":
		result := tools.Write(ctx, cfg.CWD, stringInput(input, "path"), stringInput(input, "content"))
		output, isErr = result.Output, result.IsError
		diffInfo = result.DiffInfo
	case "edit":
		result := tools.Edit(ctx, cfg.CWD, stringInput(input, "path"), stringInputAny(input, "old_text", "oldText"), stringInputAny(input, "new_text", "newText"), editInputs(input))
		output, isErr = result.Output, result.IsError
		diffInfo = result.DiffInfo
	case "memory_write":
		output = memory.HandleMemoryWrite("", input, cfg.CWD)
		isErr = strings.Contains(strings.ToLower(output), "error")
	case "memory_search":
		query := stringInput(input, "query")
		results, err := memory.Search(query, intInputAny(input, "top_k", "topK"), stringInput(input, "kind"), cfg.CWD)
		if err != nil {
			output, isErr = fmt.Sprintf("memory search error: %v", err), true
		} else {
			output = memory.FormatResults(results, query)
		}
	case "recall_compaction":
		dir := ""
		if cfg.Session != nil {
			dir = cfg.Session.Path()
		}
		output, isErr = recallCompaction(dir, stringInput(input, "id"))
	case "web_search":
		result := tools.WebSearch(ctx, stringInput(input, "query"), intInput(input, "max_results"))
		output, isErr = result.Output, result.IsError
	case "web_fetch":
		result := tools.WebFetch(ctx, stringInput(input, "url"))
		output, isErr = result.Output, result.IsError
	case "stackoverflow_search":
		result := tools.StackOverflowSearch(ctx, stringInput(input, "query"), intInput(input, "max_results"))
		output, isErr = result.Output, result.IsError
	case "git_source":
		result := tools.GitSource(ctx, cfg.CWD, stringInput(input, "repo"), stringInput(input, "ref"))
		output, isErr = result.Output, result.IsError
	default:
		output, isErr = fmt.Sprintf("unknown tool: %s", name), true
	}

	return provider.ContentBlock{
		Type:    "tool_result",
		Content: output,
		IsError: isErr,
	}, diffInfo
}

func callModel(ctx context.Context, cfg AgentConfig, messages []provider.Message, system, model string) (provider.Message, provider.Usage, error) {
	if cfg.StreamCB != nil {
		return cfg.Provider.CallStreaming(ctx, messages, system, TOOLS, model, cfg.StreamCB)
	}
	return cfg.Provider.Call(ctx, messages, system, TOOLS, model)
}

func normalizeConfig(cfg AgentConfig) AgentConfig {
	if cfg.CWD == "" {
		if cwd, err := os.Getwd(); err == nil {
			cfg.CWD = cwd
		}
	}
	if cfg.Provider == nil {
		cfg.Provider = provider.Claude
	}
	cfg.MaxSteps = maxSteps(cfg.MaxSteps)
	return cfg
}

func maxSteps(current int) int {
	if value := strings.TrimSpace(os.Getenv("MIMICODE_MAX_STEPS")); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil && parsed > 0 {
			return parsed
		}
	}
	if current > 0 {
		return current
	}
	return 25
}

func toolUseBlocks(msg provider.Message) []provider.ContentBlock {
	var out []provider.ContentBlock
	for _, block := range msg.Content {
		if block.Type == "tool_use" {
			out = append(out, block)
		}
	}
	return out
}

func msgText(msg provider.Message) string {
	var parts []string
	for _, b := range msg.Content {
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			parts = append(parts, strings.TrimSpace(b.Text))
		}
	}
	return strings.Join(parts, "\n")
}

func msgCalls(msg provider.Message) []store.CallRec {
	var calls []store.CallRec
	for _, b := range msg.Content {
		if b.Type == "tool_use" {
			calls = append(calls, store.CallRec{ID: b.ID, Name: b.Name, Input: b.Input})
		}
	}
	return calls
}

func stringInput(input map[string]any, key string) string {
	s, _ := input[key].(string)
	return s
}

func stringInputAny(input map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := stringInput(input, key); value != "" {
			return value
		}
	}
	return ""
}

func intInput(input map[string]any, key string) int {
	switch value := input[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	case json.Number:
		i, _ := value.Int64()
		return int(i)
	default:
		return 0
	}
}

func intInputAny(input map[string]any, keys ...string) int {
	for _, key := range keys {
		if value := intInput(input, key); value != 0 {
			return value
		}
	}
	return 0
}

func numberInput(input map[string]any, key string) float64 {
	switch value := input[key].(type) {
	case int:
		return float64(value)
	case int64:
		return float64(value)
	case float64:
		return value
	case json.Number:
		f, _ := value.Float64()
		return f
	default:
		return 0
	}
}

func editInputs(input map[string]any) []tools.EditOp {
	raw, ok := input["edits"].([]any)
	if !ok {
		// Some models (e.g. Opus 4.x, GLM) emit `edits` as a JSON-encoded string
		// instead of an array. Decode it rather than silently dropping the edits.
		if s, isStr := input["edits"].(string); isStr && strings.TrimSpace(s) != "" {
			if err := json.Unmarshal([]byte(s), &raw); err != nil {
				return nil
			}
		} else {
			return nil
		}
	}
	edits := make([]tools.EditOp, 0, len(raw))
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		edits = append(edits, tools.EditOp{
			OldText: stringInputAny(m, "old_text", "oldText"),
			NewText: stringInputAny(m, "new_text", "newText"),
		})
	}
	return edits
}

func recallCompaction(sessionDir, id string) (string, bool) {
	if sessionDir == "" {
		return "no active session", true
	}
	if id != "" {
		record := compactor.LoadCompaction(sessionDir, id)
		if record == nil {
			return "compaction not found: " + id, true
		}
		data, err := json.MarshalIndent(record, "", "  ")
		if err != nil {
			return fmt.Sprintf("recall compaction error: %v", err), true
		}
		return string(data), false
	}
	records := compactor.ListCompactions(sessionDir)
	if len(records) == 0 {
		return "no compactions", false
	}
	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return fmt.Sprintf("recall compactions error: %v", err), true
	}
	return string(data), false
}

func IsInterrupted(err error) bool {
	var interrupted AgentInterrupted
	return errors.As(err, &interrupted)
}

func IsStuck(err error) (AgentStuck, bool) {
	var stuck AgentStuck
	if errors.As(err, &stuck) {
		return stuck, true
	}
	return AgentStuck{}, false
}
