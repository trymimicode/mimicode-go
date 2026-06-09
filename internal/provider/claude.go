package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// apiBase is the Anthropic Messages endpoint. Overridden in tests.
var apiBase = "https://api.anthropic.com/v1/messages"

// maxTokensBase is the output budget reserved for the visible response (text +
// tool calls). When extended thinking is enabled, the thinking budget is added
// on top of this so reasoning never eats into the room for the actual answer.
const maxTokensBase = 16384

const (
	ModelOpus   = "claude-opus-4-8"
	ModelSonnet = "claude-sonnet-4-6"
	ModelHaiku  = "claude-haiku-4-5-20251001"
)

// DefaultModel returns the model from MIMICODE_MODEL env var or ModelSonnet.
// Sonnet is the documented default: fast, strong at code, and cheaper than Opus.
func DefaultModel() string {
	if m := strings.TrimSpace(os.Getenv("MIMICODE_MODEL")); m != "" {
		return m
	}
	return ModelSonnet
}

// thinkingBudget returns the extended-thinking token budget for this run,
// controlled by MIMICODE_THINKING (off|low|medium|high). Default is medium:
// enough reasoning to plan edits and self-correct without burning the wallet.
// A budget of 0 disables thinking entirely.
func thinkingBudget() int {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("MIMICODE_THINKING"))) {
	case "off", "0", "none", "false":
		return 0
	case "low":
		return 4096
	case "high":
		return 24000
	case "medium", "":
		return 10000
	default:
		return 10000
	}
}

// maxTokensFor sizes the output budget so the visible response always has room
// even after extended thinking consumes its share.
func maxTokensFor(budget int) int {
	if budget <= 0 {
		return maxTokensBase
	}
	return budget + maxTokensBase
}

// codingTemperature is sampled low for deterministic, focused edits. It is only
// sent when thinking is OFF — the API requires temperature be unset (=1) while
// extended thinking is enabled.
var codingTemperature = 0.0

// ── Internal JSON types ───────────────────────────────────────────────────────

type apiCacheControl struct {
	Type string `json:"type"`
}

var ephemeral = &apiCacheControl{Type: "ephemeral"}

type apiSystemBlock struct {
	Type         string           `json:"type"`
	Text         string           `json:"text"`
	CacheControl *apiCacheControl `json:"cache_control,omitempty"`
}

type apiTool struct {
	Name         string           `json:"name"`
	Description  string           `json:"description"`
	InputSchema  map[string]any   `json:"input_schema"`
	CacheControl *apiCacheControl `json:"cache_control,omitempty"`
}

type apiContentBlock struct {
	Type         string           `json:"type"`
	Text         string           `json:"text,omitempty"`
	ID           string           `json:"id,omitempty"`
	Name         string           `json:"name,omitempty"`
	Input        map[string]any   `json:"input,omitempty"`
	Content      interface{}      `json:"content,omitempty"` // string | nil
	IsError      bool             `json:"is_error,omitempty"`
	ToolUseID    string           `json:"tool_use_id,omitempty"`
	Thinking     string           `json:"thinking,omitempty"`
	Signature    string           `json:"signature,omitempty"`
	Data         string           `json:"data,omitempty"` // redacted_thinking payload
	CacheControl *apiCacheControl `json:"cache_control,omitempty"`
}

type apiMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type apiThinking struct {
	Type         string `json:"type"` // "enabled"
	BudgetTokens int    `json:"budget_tokens"`
}

type requestBody struct {
	Model       string           `json:"model"`
	MaxTokens   int              `json:"max_tokens"`
	System      []apiSystemBlock `json:"system,omitempty"`
	Tools       []apiTool        `json:"tools,omitempty"`
	Messages    []apiMessage     `json:"messages"`
	Stream      bool             `json:"stream,omitempty"`
	Temperature *float64         `json:"temperature,omitempty"`
	Thinking    *apiThinking     `json:"thinking,omitempty"`
}

type apiUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

type apiResponse struct {
	Role       string            `json:"role"`
	Content    []apiContentBlock `json:"content"`
	Usage      apiUsage          `json:"usage"`
	StopReason string            `json:"stop_reason"`
}

type apiErrorResp struct {
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

// ── Request building ──────────────────────────────────────────────────────────

func toAPIBlock(cb ContentBlock) apiContentBlock {
	ab := apiContentBlock{
		Type:      cb.Type,
		Text:      cb.Text,
		ID:        cb.ID,
		Name:      cb.Name,
		Input:     cb.Input,
		IsError:   cb.IsError,
		ToolUseID: cb.ToolUseID,
		Thinking:  cb.Thinking,
		Signature: cb.Signature,
		Data:      cb.Data,
	}
	if cb.Content != "" {
		ab.Content = cb.Content
	}
	return ab
}

// isThinkingBlock reports whether a block is reasoning output. The API rejects
// cache_control markers on these, so the caching pass must skip them.
func isThinkingBlock(b apiContentBlock) bool {
	return b.Type == "thinking" || b.Type == "redacted_thinking"
}

func marshalBlocks(blocks []apiContentBlock) json.RawMessage {
	raw, err := json.Marshal(blocks)
	if err != nil {
		panic(fmt.Sprintf("provider: marshal blocks: %v", err))
	}
	return raw
}

func buildRequest(messages []Message, system string, tools []ToolSchema, model string, stream bool) requestBody {
	// System: single block marked ephemeral.
	var sysBlocks []apiSystemBlock
	if system != "" {
		sysBlocks = []apiSystemBlock{{Type: "text", Text: system, CacheControl: ephemeral}}
	}

	// Tools: last one marked ephemeral.
	apiTools := make([]apiTool, len(tools))
	for i, t := range tools {
		apiTools[i] = apiTool{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema}
	}
	if len(apiTools) > 0 {
		apiTools[len(apiTools)-1].CacheControl = ephemeral
	}

	// Messages: deep-copy; mark last cacheable block of last message ephemeral.
	// Thinking blocks cannot carry cache_control, so fall back to the nearest
	// preceding non-thinking block.
	apiMsgs := make([]apiMessage, len(messages))
	for i, msg := range messages {
		blocks := make([]apiContentBlock, len(msg.Content))
		for j, cb := range msg.Content {
			blocks[j] = toAPIBlock(cb)
		}
		if i == len(messages)-1 {
			for k := len(blocks) - 1; k >= 0; k-- {
				if !isThinkingBlock(blocks[k]) {
					blocks[k].CacheControl = ephemeral
					break
				}
			}
		}
		apiMsgs[i] = apiMessage{Role: msg.Role, Content: marshalBlocks(blocks)}
	}

	body := requestBody{
		Model:     model,
		MaxTokens: maxTokensFor(thinkingBudget()),
		System:    sysBlocks,
		Tools:     apiTools,
		Messages:  apiMsgs,
		Stream:    stream,
	}

	// Extended thinking and temperature are mutually exclusive: the API requires
	// temperature be unset while thinking is enabled.
	if budget := thinkingBudget(); budget > 0 {
		body.Thinking = &apiThinking{Type: "enabled", BudgetTokens: budget}
	} else {
		body.Temperature = &codingTemperature
	}

	return body
}

// ── HTTP layer ────────────────────────────────────────────────────────────────

func sendRequest(ctx context.Context, body requestBody, baseURL, envKeyName string) (*http.Response, error) {
	apiKey := os.Getenv(envKeyName)
	if apiKey == "" {
		return nil, fmt.Errorf("%s is not set", envKeyName)
	}

	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")
	// Prompt caching is GA (no beta header needed). When extended thinking is on,
	// opt into interleaved thinking so the model can reason between tool calls.
	if thinkingBudget() > 0 {
		req.Header.Set("anthropic-beta", "interleaved-thinking-2025-05-14")
	}

	return http.DefaultClient.Do(req)
}

func toUsage(au apiUsage) Usage {
	return Usage{
		InputTokens:  au.InputTokens,
		OutputTokens: au.OutputTokens,
		CacheRead:    au.CacheReadInputTokens,
		CacheWrite:   au.CacheCreationInputTokens,
	}
}

func readAPIError(body []byte) string {
	var e apiErrorResp
	if json.Unmarshal(body, &e) == nil && e.Error.Message != "" {
		return e.Error.Message
	}
	return string(body)
}

// ── CallClaude ────────────────────────────────────────────────────────────────

func callClaudeWith(ctx context.Context, messages []Message, system string, tools []ToolSchema, model, baseURL, envKeyName string) (Message, Usage, error) {
	req := buildRequest(messages, system, tools, model, false)

	resp, err := sendRequest(ctx, req, baseURL, envKeyName)
	if err != nil {
		return Message{}, Usage{}, fmt.Errorf("send: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return Message{}, Usage{}, fmt.Errorf("read body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return Message{}, Usage{}, fmt.Errorf("API %d: %s", resp.StatusCode, readAPIError(raw))
	}

	var apiResp apiResponse
	if err := json.Unmarshal(raw, &apiResp); err != nil {
		return Message{}, Usage{}, fmt.Errorf("decode response: %w", err)
	}

	out := Message{Role: apiResp.Role}
	for _, ab := range apiResp.Content {
		blk := ContentBlock{
			Type:      ab.Type,
			Text:      ab.Text,
			ID:        ab.ID,
			Name:      ab.Name,
			Input:     ab.Input,
			IsError:   ab.IsError,
			ToolUseID: ab.ToolUseID,
			Thinking:  ab.Thinking,
			Signature: ab.Signature,
			Data:      ab.Data,
		}
		if s, ok := ab.Content.(string); ok {
			blk.Content = s
		}
		out.Content = append(out.Content, blk)
	}

	warnIfTruncated(apiResp.StopReason)

	u := toUsage(apiResp.Usage)
	saveUsage(u)
	return out, u, nil
}

// CallClaude sends a synchronous request to the Anthropic Messages API.
func CallClaude(ctx context.Context, messages []Message, system string, tools []ToolSchema, model string) (Message, Usage, error) {
	return callClaudeWith(ctx, messages, system, tools, model, apiBase, "ANTHROPIC_API_KEY")
}

// warnIfTruncated surfaces a max_tokens cutoff instead of letting a truncated
// (and likely broken) response pass silently as a finished turn.
func warnIfTruncated(stopReason string) {
	if stopReason == "max_tokens" {
		fmt.Fprintln(os.Stderr, "mimicode: warning — response hit max_tokens and was truncated; "+
			"raise MIMICODE_THINKING headroom or split the task into smaller steps")
	}
}

// ── CallClaudeStreaming ───────────────────────────────────────────────────────

// streamBlock tracks state for a single content block during streaming.
type streamBlock struct {
	btype   string
	id      string
	name    string
	data    string // redacted_thinking payload
	text    strings.Builder
	sig     strings.Builder // thinking signature
	jsonBuf strings.Builder
}

// callClaudeStreamingWith sends a streaming request and delivers events via cb.
// On context cancellation, remaining bytes are drained and the partial
// assembled message is returned alongside ctx.Err().
func callClaudeStreamingWith(ctx context.Context, messages []Message, system string, tools []ToolSchema, model, baseURL, envKeyName string, cb StreamCallback) (Message, Usage, error) {
	req := buildRequest(messages, system, tools, model, true)

	resp, err := sendRequest(ctx, req, baseURL, envKeyName)
	if err != nil {
		return Message{}, Usage{}, fmt.Errorf("send: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return Message{}, Usage{}, fmt.Errorf("API %d: %s", resp.StatusCode, readAPIError(body))
	}

	blockMap := make(map[int]*streamBlock)
	var blockOrder []int
	var usage Usage
	var stopReason string

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 256*1024), 256*1024)

	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := line[6:]
		if payload == "[DONE]" {
			break
		}

		var evt map[string]any
		if err := json.Unmarshal([]byte(payload), &evt); err != nil {
			continue
		}

		switch evtType, _ := evt["type"].(string); evtType {

		case "message_start":
			if msg, ok := evt["message"].(map[string]any); ok {
				if u, ok := msg["usage"].(map[string]any); ok {
					usage.InputTokens = jsonInt(u, "input_tokens")
					usage.CacheWrite = jsonInt(u, "cache_creation_input_tokens")
					usage.CacheRead = jsonInt(u, "cache_read_input_tokens")
				}
			}

		case "content_block_start":
			idx := jsonInt(evt, "index")
			cbData, _ := evt["content_block"].(map[string]any)
			btype, _ := cbData["type"].(string)

			blk := &streamBlock{btype: btype}
			switch btype {
			case "tool_use":
				blk.id, _ = cbData["id"].(string)
				blk.name, _ = cbData["name"].(string)
			case "redacted_thinking":
				blk.data, _ = cbData["data"].(string)
			}
			blockMap[idx] = blk
			blockOrder = append(blockOrder, idx)

			if cb != nil {
				switch btype {
				case "text":
					cb(TextStart, map[string]any{"index": idx})
				case "thinking":
					cb(ThinkingStart, map[string]any{"index": idx})
				case "tool_use":
					cb(ToolStart, map[string]any{"index": idx, "id": blk.id, "name": blk.name})
				}
			}

		case "content_block_delta":
			idx := jsonInt(evt, "index")
			blk := blockMap[idx]
			if blk == nil {
				continue
			}
			delta, _ := evt["delta"].(map[string]any)
			deltaType, _ := delta["type"].(string)

			switch deltaType {
			case "text_delta":
				text, _ := delta["text"].(string)
				blk.text.WriteString(text)
				if cb != nil {
					cb(TextDelta, map[string]any{"index": idx, "text": text})
				}
			case "thinking_delta":
				thinking, _ := delta["thinking"].(string)
				blk.text.WriteString(thinking)
				if cb != nil {
					cb(ThinkingDelta, map[string]any{"index": idx, "text": thinking})
				}
			case "signature_delta":
				sig, _ := delta["signature"].(string)
				blk.sig.WriteString(sig)
			case "input_json_delta":
				partial, _ := delta["partial_json"].(string)
				blk.jsonBuf.WriteString(partial)
			}

		case "content_block_stop":
			idx := jsonInt(evt, "index")
			blk := blockMap[idx]
			if blk == nil || blk.btype != "tool_use" {
				break
			}
			if cb != nil {
				var input map[string]any
				if blk.jsonBuf.Len() > 0 {
					json.Unmarshal([]byte(blk.jsonBuf.String()), &input) //nolint:errcheck
				}
				cb(ToolComplete, map[string]any{"index": idx, "id": blk.id, "name": blk.name, "input": input})
			}

		case "message_delta":
			if u, ok := evt["usage"].(map[string]any); ok {
				usage.OutputTokens = jsonInt(u, "output_tokens")
			}
			if d, ok := evt["delta"].(map[string]any); ok {
				if sr, _ := d["stop_reason"].(string); sr != "" {
					stopReason = sr
				}
			}

		case "message_stop":
			if cb != nil {
				cb("message_stop", map[string]any{})
			}
		}
	}

	if err := sc.Err(); err != nil && ctx.Err() == nil {
		return Message{}, usage, fmt.Errorf("stream read: %w", err)
	}

	// Assemble final message in block order, deduplicating.
	out := Message{Role: "assistant"}
	seen := make(map[int]bool)
	for _, idx := range blockOrder {
		if seen[idx] {
			continue
		}
		seen[idx] = true
		blk := blockMap[idx]
		if blk == nil {
			continue
		}
		switch blk.btype {
		case "text":
			out.Content = append(out.Content, ContentBlock{Type: "text", Text: blk.text.String()})
		case "thinking":
			out.Content = append(out.Content, ContentBlock{
				Type:      "thinking",
				Thinking:  blk.text.String(),
				Signature: blk.sig.String(),
			})
		case "redacted_thinking":
			out.Content = append(out.Content, ContentBlock{Type: "redacted_thinking", Data: blk.data})
		case "tool_use":
			var input map[string]any
			if blk.jsonBuf.Len() > 0 {
				json.Unmarshal([]byte(blk.jsonBuf.String()), &input) //nolint:errcheck
			}
			out.Content = append(out.Content, ContentBlock{
				Type:  "tool_use",
				ID:    blk.id,
				Name:  blk.name,
				Input: input,
			})
		}
	}

	saveUsage(usage)

	if ctx.Err() != nil {
		return out, usage, ctx.Err()
	}
	warnIfTruncated(stopReason)
	return out, usage, nil
}

// CallClaudeStreaming sends a streaming request and delivers events via cb.
func CallClaudeStreaming(ctx context.Context, messages []Message, system string, tools []ToolSchema, model string, cb StreamCallback) (Message, Usage, error) {
	return callClaudeStreamingWith(ctx, messages, system, tools, model, apiBase, "ANTHROPIC_API_KEY", cb)
}

// jsonInt extracts an int from a map[string]any (JSON numbers decode as float64).
func jsonInt(m map[string]any, key string) int {
	if v, ok := m[key].(float64); ok {
		return int(v)
	}
	return 0
}

// ── claudeProvider ────────────────────────────────────────────────────────────

type claudeProvider struct {
	baseURL    string
	envKeyName string
	model      string // empty = use DefaultModel()
}

func (p claudeProvider) Call(ctx context.Context, messages []Message, system string, tools []ToolSchema, model string) (Message, Usage, error) {
	return callClaudeWith(ctx, messages, system, tools, model, p.baseURL, p.envKeyName)
}

func (p claudeProvider) CallStreaming(ctx context.Context, messages []Message, system string, tools []ToolSchema, model string, cb StreamCallback) (Message, Usage, error) {
	return callClaudeStreamingWith(ctx, messages, system, tools, model, p.baseURL, p.envKeyName, cb)
}

func (p claudeProvider) DefaultModel() string {
	if p.model != "" {
		return p.model
	}
	return DefaultModel()
}

// NewClaudeCompatible returns a Provider that speaks the Anthropic Messages
// API against an alternative base URL (e.g. MiniMax, OpenRouter).
func NewClaudeCompatible(baseURL, envKeyName, defaultModel string) Provider {
	return claudeProvider{baseURL: baseURL, envKeyName: envKeyName, model: defaultModel}
}

// Claude is the Anthropic provider. Pass it in AgentConfig.Provider to use Claude.
var Claude Provider = claudeProvider{baseURL: apiBase, envKeyName: "ANTHROPIC_API_KEY"}
