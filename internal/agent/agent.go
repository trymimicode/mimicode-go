package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
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

const SYSTEM_PROMPT = `You are a coding agent in a minimal harness called mimicode.

You have seven tools: read, bash, edit, write, web_search, web_fetch, stackoverflow_search. Use them deliberately.

SEARCH RULES (non-negotiable):
- Use 'rg' (ripgrep) for every search. rg respects .gitignore by default.
- List files: rg --files (not 'find .' or 'ls -R')
- List by extension: rg --files -t py (not 'find . -name '*.py'')
- Search content: rg 'pattern' (not 'grep -r')
- Scope to a dir: rg 'pattern' path/
- Case-insensitive: rg -i 'pattern'
- With line numbers: rg -n 'pattern' (on by default for content search)
- List matching files: rg -l 'pattern'
Never run 'find', 'grep -r', 'ls -R', or 'cat <codefile>'. Use the 'read' tool for code files.
ALWAYS EXCLUDE from exploration: .venv/ .git/ node_modules/ sessions/ __pycache__/ dist/ build/ .pytest_cache/

EDITING RULES:
- 'read' before 'edit'. Always.
- 'edit' requires old_text to match exactly once. Include 2-3 lines of surrounding context so the match is unique.
- For multiple changes to the SAME file in one logical operation, prefer ONE 'edit' call with
  'edits=[{old_text, new_text}, ...]' over multiple sequential 'edit' calls. Batched edits are
  atomic: all succeed or none apply.
- 'write' only for new files or full rewrites. Never for partial changes.

MEMORY RULES:
- After a turn that modified files OR made a meaningful decision, call 'memory_write' with a one-sentence
  summary, the touched component name, and a 'change_entry' describing what/why.
- For purely read-only / exploratory turns that produced no carry-forward insight, skip memory_write.
- Do not write speculative or vague summaries.
- When the user asks about something that may have been worked on before ("how did we previously...",
  "have we built...", "where did we decide..."), call 'memory_search' before reading source files.

WEB RULES:
- Use web_search to find docs, examples, or answers. Add site: filters to scope (site:stackoverflow.com, site:pkg.go.dev, site:github.com).
- Use web_fetch to get full content from a URL. Automatically handles GitHub issues, Reddit, HN, and Stack Overflow question URLs.
- Use stackoverflow_search when debugging errors or looking for usage examples — it returns questions AND their top answers inline.
- Always prefer stackoverflow_search over web_search for programming questions; it saves a round-trip.

DEBUGGING RULES:
- Before editing any file in response to an error, determine whether the error is in the code or
  in how it was invoked.
- 'command not found: <file>.py' means the shell can't execute the file as a program — the script's
  code is almost certainly fine. ALWAYS explain 'python <file>.py' as the fix. Do NOT edit the file.
- Non-zero exit codes from test runners (pytest, etc.) are expected when tests fail — read the output.

STYLE:
- Prefer one targeted tool call over a broad one. Scope searches.
- Tool output is capped at 100KB. If you hit that, your scope was too wide.
- Be concise. Cite file:line where relevant.
- Do NOT create markdown (.md) files to summarize what is happening. Respond directly.
- Add Diffs for different files with which files has been changed and which line has been added.
- Remove redundant word usage like 'Now I will', 'Perfect! Now', etc.`

type AgentConfig struct {
	CWD      string
	MaxSteps int
	Session  *store.Session // nil = no logging
	StreamCB provider.StreamCallback
	Model    string // empty = use provider.DefaultModel()
}

type AgentInterrupted struct{}

func (AgentInterrupted) Error() string { return "agent interrupted" }

var (
	callClaude          = provider.CallClaude
	callClaudeStreaming = provider.CallClaudeStreaming
)

var TOOLS = []provider.ToolSchema{
	{
		Name:        "bash",
		Description: "Run a shell command in the current working directory. Use for tests, builds, searches, and other terminal tasks.",
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
		Description: "Apply exact find-and-replace edits to an existing text file.",
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
}

func BuildSystem(cwd string) string {
	var b strings.Builder
	b.WriteString(SYSTEM_PROMPT)
	fmt.Fprintf(&b, "\n\nCurrent date: %s", time.Now().Format("2006-01-02"))
	fmt.Fprintf(&b, "\nCurrent working directory: %s", cwd)

	if repo := repomap.Cached(); repo != "" {
		fmt.Fprintf(&b, "\n\n## Repository map\n%s", repo)
	}
	if rules := memory.LoadRules(cwd); rules != "" {
		fmt.Fprintf(&b, "\n\n## Behavioral rules\n%s", rules)
	}
	if mem := memory.LoadMemory(cwd); mem != "" {
		fmt.Fprintf(&b, "\n\n## Memory\n%s", mem)
	}
	return b.String()
}

func AgentTurn(ctx context.Context, cfg AgentConfig, userMsg string, messages []provider.Message) ([]provider.Message, error) {
	cfg = normalizeConfig(cfg)

	var turn int
	if cfg.Session != nil {
		turn = cfg.Session.LogUser(userMsg)
	}

	messages = append(messages, provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{{Type: "text", Text: userMsg}},
	})

	system := BuildSystem(cfg.CWD)
	model := cfg.Model
	if model == "" {
		model = provider.DefaultModel()
	}
	sessionDir := ""
	if cfg.Session != nil {
		sessionDir = cfg.Session.Path()
	}

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
		for _, tu := range toolUses {
			if err := ctx.Err(); err != nil {
				return messages, AgentInterrupted{}
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
		}
		messages = append(messages, provider.Message{Role: "user", Content: results})
	}

	if cfg.Session != nil {
		cfg.Session.LogTurnEnd(turn, cfg.MaxSteps, "max_steps")
	}
	repomap.RefreshAsync(cfg.CWD)
	return messages, nil
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
		return callClaudeStreaming(ctx, messages, system, TOOLS, model, cfg.StreamCB)
	}
	return callClaude(ctx, messages, system, TOOLS, model)
}

func normalizeConfig(cfg AgentConfig) AgentConfig {
	if cfg.CWD == "" {
		if cwd, err := os.Getwd(); err == nil {
			cfg.CWD = cwd
		}
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
		return nil
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
