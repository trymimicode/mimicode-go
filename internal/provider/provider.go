package provider

import "sync"

// ── Stream event constants ────────────────────────────────────────────────────

type StreamEvent = string

const (
	TextDelta      StreamEvent = "text_delta"
	TextStart      StreamEvent = "text_start"
	ThinkingStart  StreamEvent = "thinking_start"
	ThinkingDelta  StreamEvent = "thinking_delta"
	ToolStart      StreamEvent = "tool_start"
	ToolComplete   StreamEvent = "tool_complete"
	ToolExecStart  StreamEvent = "tool_exec_start"
	ToolExecResult StreamEvent = "tool_exec_result"
)

// ── Public types ──────────────────────────────────────────────────────────────

// ContentBlock is one element of a Message's content list.
type ContentBlock struct {
	Type      string         // "text" | "tool_use" | "tool_result" | "thinking" | "redacted_thinking"
	Text      string         // type=text
	ID        string         // type=tool_use
	Name      string         // type=tool_use
	Input     map[string]any // type=tool_use
	Content   string         // type=tool_result
	IsError   bool           // type=tool_result
	ToolUseID string         // type=tool_result
	Thinking  string         // type=thinking
	Signature string         // type=thinking (cryptographic signature; must be sent back verbatim)
	Data      string         // type=redacted_thinking
}

// Message is one conversation turn.
type Message struct {
	Role    string // "user" | "assistant"
	Content []ContentBlock
}

// ToolSchema describes a tool available to a provider.
type ToolSchema struct {
	Name        string
	Description string
	InputSchema map[string]any
}

// CacheControl marks a block for prompt caching.
type CacheControl struct {
	Type string // "ephemeral"
}

// SystemCacheBreak is an in-band marker the caller embeds in the system string
// to separate the static persona (a stable, cacheable prefix) from the volatile
// per-turn context (env, repomap, rules, memory). The Claude builder splits on
// it into separate system blocks so the persona prefix stays cached even when
// the context changes; the OpenAI builder collapses it back to a blank line.
// It is a control char (US, 0x1F) that never appears in prompt prose, and it is
// always stripped before the request reaches the model.
const SystemCacheBreak = "\x1f"

// Usage records token consumption for one API call.
type Usage struct {
	InputTokens  int
	OutputTokens int
	CacheRead    int
	CacheWrite   int
}

// StreamCallback receives events during streaming.
type StreamCallback func(eventType string, data map[string]any)

// ── Package-level usage store ─────────────────────────────────────────────────

var (
	usageMu     sync.Mutex
	storedUsage Usage
)

// LastUsage returns the most recently observed API usage.
func LastUsage() Usage {
	usageMu.Lock()
	defer usageMu.Unlock()
	return storedUsage
}

func saveUsage(u Usage) {
	usageMu.Lock()
	storedUsage = u
	usageMu.Unlock()
}
