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
	"sync"
)

// apiBase is the Anthropic Messages endpoint. Overridden in tests.
var apiBase = "https://api.anthropic.com/v1/messages"

const maxTokens = 8192

const (
	ModelOpus   = "claude-opus-4-20250514"
	ModelSonnet = "claude-sonnet-4-5-20250929"
	ModelHaiku  = "claude-haiku-4-5-20251001"
)

// DefaultModel returns the model from MIMICODE_MODEL env var or ModelOpus.
func DefaultModel() string {
	if m := strings.TrimSpace(os.Getenv("MIMICODE_MODEL")); m != "" {
		return m
	}
	return ModelOpus
}

// ── Stream event constants ────────────────────────────────────────────────────

type StreamEvent = string

const (
	TextDelta      StreamEvent = "text_delta"
	TextStart      StreamEvent = "text_start"
	ToolStart      StreamEvent = "tool_start"
	ToolComplete   StreamEvent = "tool_complete"
	ToolExecStart  StreamEvent = "tool_exec_start"
	ToolExecResult StreamEvent = "tool_exec_result"
)

// ── Public types ──────────────────────────────────────────────────────────────

// ContentBlock is one element of a Message's content list.
type ContentBlock struct {
	Type      string         // "text" | "tool_use" | "tool_result"
	Text      string         // type=text
	ID        string         // type=tool_use
	Name      string         // type=tool_use
	Input     map[string]any // type=tool_use
	Content   string         // type=tool_result
	IsError   bool           // type=tool_result
	ToolUseID string         // type=tool_result
}

// Message is one conversation turn.
type Message struct {
	Role    string // "user" | "assistant"
	Content []ContentBlock
}

// ToolSchema describes a tool available to Claude.
type ToolSchema struct {
	Name        string
	Description string
	InputSchema map[string]any
}

// CacheControl marks a block for prompt caching.
type CacheControl struct {
	Type string // "ephemeral"
}

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
	CacheControl *apiCacheControl `json:"cache_control,omitempty"`
}

type apiMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type requestBody struct {
	Model     string           `json:"model"`
	MaxTokens int              `json:"max_tokens"`
	System    []apiSystemBlock `json:"system,omitempty"`
	Tools     []apiTool        `json:"tools,omitempty"`
	Messages  []apiMessage     `json:"messages"`
	Stream    bool             `json:"stream,omitempty"`
}

type apiUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

type apiResponse struct {
	Role    string            `json:"role"`
	Content []apiContentBlock `json:"content"`
	Usage   apiUsage          `json:"usage"`
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
	}
	if cb.Content != "" {
		ab.Content = cb.Content
	}
	return ab
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

	// Messages: deep-copy; mark last block of last message ephemeral.
	// If the last message has no blocks, nothing to mark.
	apiMsgs := make([]apiMessage, len(messages))
	for i, msg := range messages {
		blocks := make([]apiContentBlock, len(msg.Content))
		for j, cb := range msg.Content {
			blocks[j] = toAPIBlock(cb)
		}
		if i == len(messages)-1 && len(blocks) > 0 {
			blocks[len(blocks)-1].CacheControl = ephemeral
		}
		apiMsgs[i] = apiMessage{Role: msg.Role, Content: marshalBlocks(blocks)}
	}

	return requestBody{
		Model:     model,
		MaxTokens: maxTokens,
		System:    sysBlocks,
		Tools:     apiTools,
		Messages:  apiMsgs,
		Stream:    stream,
	}
}

// ── HTTP layer ────────────────────────────────────────────────────────────────

func sendRequest(ctx context.Context, body requestBody) (*http.Response, error) {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("ANTHROPIC_API_KEY is not set")
	}

	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiBase, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")
	req.Header.Set("anthropic-beta", "prompt-caching-2024-07-31")

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

// CallClaude sends a synchronous request to the Anthropic Messages API.
func CallClaude(ctx context.Context, messages []Message, system string, tools []ToolSchema, model string) (Message, Usage, error) {
	req := buildRequest(messages, system, tools, model, false)

	resp, err := sendRequest(ctx, req)
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
		}
		if s, ok := ab.Content.(string); ok {
			blk.Content = s
		}
		out.Content = append(out.Content, blk)
	}

	u := toUsage(apiResp.Usage)
	saveUsage(u)
	return out, u, nil
}

// ── CallClaudeStreaming ───────────────────────────────────────────────────────

// streamBlock tracks state for a single content block during streaming.
type streamBlock struct {
	btype   string
	id      string
	name    string
	text    strings.Builder
	jsonBuf strings.Builder
}

// CallClaudeStreaming sends a streaming request and delivers events via cb.
// On context cancellation, remaining bytes are drained and the partial
// assembled message is returned alongside ctx.Err().
func CallClaudeStreaming(ctx context.Context, messages []Message, system string, tools []ToolSchema, model string, cb StreamCallback) (Message, Usage, error) {
	req := buildRequest(messages, system, tools, model, true)

	resp, err := sendRequest(ctx, req)
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
			if btype == "tool_use" {
				blk.id, _ = cbData["id"].(string)
				blk.name, _ = cbData["name"].(string)
			}
			blockMap[idx] = blk
			blockOrder = append(blockOrder, idx)

			if cb != nil {
				switch btype {
				case "text":
					cb(TextStart, map[string]any{"index": idx})
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
	return out, usage, nil
}

// jsonInt extracts an int from a map[string]any (JSON numbers decode as float64).
func jsonInt(m map[string]any, key string) int {
	if v, ok := m[key].(float64); ok {
		return int(v)
	}
	return 0
}
