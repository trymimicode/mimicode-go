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

// ── OpenAI-compatible wire types ──────────────────────────────────────────────

type oaiFunctionDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type oaiTool struct {
	Type     string         `json:"type"` // "function"
	Function oaiFunctionDef `json:"function"`
}

type oaiToolCall struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"` // "function"
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"` // JSON-encoded string
	} `json:"function"`
}

type oaiMsg struct {
	Role       string        `json:"role"`
	Content    interface{}   `json:"content,omitempty"` // string | nil
	ToolCalls  []oaiToolCall `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
	Name       string        `json:"name,omitempty"`
}

type oaiRequest struct {
	Model    string    `json:"model"`
	Messages []oaiMsg  `json:"messages"`
	Tools    []oaiTool `json:"tools,omitempty"`
	Stream   bool      `json:"stream,omitempty"`
}

type oaiChoice struct {
	Message      oaiMsg `json:"message"`
	FinishReason string `json:"finish_reason"`
}

type oaiResponse struct {
	Choices []oaiChoice `json:"choices"`
	Usage   struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

type oaiChunkDelta struct {
	Role      string        `json:"role"`
	Content   *string       `json:"content"`
	ToolCalls []oaiToolCall `json:"tool_calls"`
}

type oaiChunkChoice struct {
	Index        int           `json:"index"`
	Delta        oaiChunkDelta `json:"delta"`
	FinishReason *string       `json:"finish_reason"`
}

type oaiChunk struct {
	Choices []oaiChunkChoice `json:"choices"`
}

// ── Conversion helpers ────────────────────────────────────────────────────────

// toOAIMessages converts our message list + system string into OpenAI messages.
// tool_result blocks become separate role=tool messages.
func toOAIMessages(messages []Message, system string) []oaiMsg {
	var out []oaiMsg
	if system != "" {
		// OpenAI has no cache breakpoints; fold the marker back into plain text.
		system = strings.ReplaceAll(system, SystemCacheBreak, "\n\n")
		out = append(out, oaiMsg{Role: "system", Content: system})
	}
	for _, m := range messages {
		switch m.Role {
		case "user":
			// Expand: text blocks → one user message, tool_result blocks → role=tool messages.
			var textParts []string
			for _, b := range m.Content {
				switch b.Type {
				case "text":
					textParts = append(textParts, b.Text)
				case "tool_result":
					content := b.Content
					if b.IsError {
						content = "error: " + content
					}
					out = append(out, oaiMsg{
						Role:       "tool",
						Content:    content,
						ToolCallID: b.ToolUseID,
					})
				}
			}
			if len(textParts) > 0 {
				out = append(out, oaiMsg{Role: "user", Content: strings.Join(textParts, "\n")})
			}
		case "assistant":
			msg := oaiMsg{Role: "assistant"}
			var textParts []string
			for _, b := range m.Content {
				switch b.Type {
				case "text":
					textParts = append(textParts, b.Text)
				case "tool_use":
					args, _ := json.Marshal(b.Input)
					msg.ToolCalls = append(msg.ToolCalls, oaiToolCall{
						ID:   b.ID,
						Type: "function",
						Function: struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						}{Name: b.Name, Arguments: string(args)},
					})
				}
			}
			if len(textParts) > 0 {
				msg.Content = strings.Join(textParts, "\n")
			}
			out = append(out, msg)
		}
	}
	return out
}

// toOAITools converts our ToolSchema slice into OpenAI tool definitions.
func toOAITools(tools []ToolSchema) []oaiTool {
	out := make([]oaiTool, len(tools))
	for i, t := range tools {
		out[i] = oaiTool{
			Type: "function",
			Function: oaiFunctionDef{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			},
		}
	}
	return out
}

// fromOAIMessage converts an OpenAI response message back to our Message type.
func fromOAIMessage(m oaiMsg) Message {
	out := Message{Role: "assistant"}
	if m.Content != nil {
		if s, ok := m.Content.(string); ok && s != "" {
			out.Content = append(out.Content, ContentBlock{Type: "text", Text: s})
		}
	}
	for _, tc := range m.ToolCalls {
		var input map[string]any
		json.Unmarshal([]byte(tc.Function.Arguments), &input) //nolint:errcheck
		out.Content = append(out.Content, ContentBlock{
			Type:  "tool_use",
			ID:    tc.ID,
			Name:  tc.Function.Name,
			Input: input,
		})
	}
	return out
}

// ── HTTP layer ────────────────────────────────────────────────────────────────

func (p openaiProvider) sendRequest(ctx context.Context, body oaiRequest) (*http.Response, error) {
	apiKey := p.apiKey
	if apiKey == "" && p.envKeyName != "" {
		apiKey = os.Getenv(p.envKeyName)
	}
	if apiKey == "" {
		return nil, fmt.Errorf("API key is not set for provider at %s", p.baseURL)
	}

	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}

	url := strings.TrimRight(p.baseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	return http.DefaultClient.Do(req)
}

// ── openaiProvider ────────────────────────────────────────────────────────────

type openaiProvider struct {
	baseURL      string
	apiKey       string // explicit key; takes precedence over envKeyName
	envKeyName   string // env var to read when apiKey is empty
	defaultModel string
}

func (p openaiProvider) DefaultModel() string { return p.defaultModel }

func (p openaiProvider) Call(ctx context.Context, messages []Message, system string, tools []ToolSchema, model string) (Message, Usage, error) {
	body := oaiRequest{
		Model:    model,
		Messages: toOAIMessages(messages, system),
		Tools:    toOAITools(tools),
	}

	resp, err := p.sendRequest(ctx, body)
	if err != nil {
		return Message{}, Usage{}, fmt.Errorf("send: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return Message{}, Usage{}, fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return Message{}, Usage{}, fmt.Errorf("API %d: %s", resp.StatusCode, readOAIError(raw))
	}

	var apiResp oaiResponse
	if err := json.Unmarshal(raw, &apiResp); err != nil {
		return Message{}, Usage{}, fmt.Errorf("decode response: %w", err)
	}
	if len(apiResp.Choices) == 0 {
		return Message{}, Usage{}, fmt.Errorf("empty choices in response")
	}

	u := Usage{
		InputTokens:  apiResp.Usage.PromptTokens,
		OutputTokens: apiResp.Usage.CompletionTokens,
	}
	saveUsage(u)
	return fromOAIMessage(apiResp.Choices[0].Message), u, nil
}

func (p openaiProvider) CallStreaming(ctx context.Context, messages []Message, system string, tools []ToolSchema, model string, cb StreamCallback) (Message, Usage, error) {
	body := oaiRequest{
		Model:    model,
		Messages: toOAIMessages(messages, system),
		Tools:    toOAITools(tools),
		Stream:   true,
	}

	resp, err := p.sendRequest(ctx, body)
	if err != nil {
		return Message{}, Usage{}, fmt.Errorf("send: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return Message{}, Usage{}, fmt.Errorf("API %d: %s", resp.StatusCode, readOAIError(raw))
	}

	// Per-tool-call accumulator keyed by tool_calls index.
	type toolAcc struct {
		id      string
		name    string
		argsBuf strings.Builder
	}
	tools_ := make(map[int]*toolAcc)
	var toolOrder []int

	var textBuf strings.Builder
	textStarted := false

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

		var chunk oaiChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			continue
		}
		if len(chunk.Choices) == 0 {
			continue
		}

		delta := chunk.Choices[0].Delta

		// Text delta.
		if delta.Content != nil && *delta.Content != "" {
			if !textStarted {
				textStarted = true
				if cb != nil {
					cb(TextStart, map[string]any{"index": 0})
				}
			}
			textBuf.WriteString(*delta.Content)
			if cb != nil {
				cb(TextDelta, map[string]any{"index": 0, "text": *delta.Content})
			}
		}

		// Tool call deltas.
		for _, tc := range delta.ToolCalls {
			idx := tc.Index
			if _, exists := tools_[idx]; !exists {
				tools_[idx] = &toolAcc{id: tc.ID, name: tc.Function.Name}
				toolOrder = append(toolOrder, idx)
				if cb != nil {
					cb(ToolStart, map[string]any{"index": idx, "id": tc.ID, "name": tc.Function.Name})
				}
			}
			acc := tools_[idx]
			// ID and name may arrive only on the first delta.
			if tc.ID != "" {
				acc.id = tc.ID
			}
			if tc.Function.Name != "" {
				acc.name = tc.Function.Name
			}
			acc.argsBuf.WriteString(tc.Function.Arguments)
		}
	}

	if err := sc.Err(); err != nil && ctx.Err() == nil {
		return Message{}, Usage{}, fmt.Errorf("stream read: %w", err)
	}

	// Emit ToolComplete for each accumulated tool call.
	for _, idx := range toolOrder {
		acc := tools_[idx]
		var input map[string]any
		if acc.argsBuf.Len() > 0 {
			json.Unmarshal([]byte(acc.argsBuf.String()), &input) //nolint:errcheck
		}
		if cb != nil {
			cb(ToolComplete, map[string]any{"index": idx, "id": acc.id, "name": acc.name, "input": input})
		}
	}

	// Assemble final message.
	out := Message{Role: "assistant"}
	if textBuf.Len() > 0 {
		out.Content = append(out.Content, ContentBlock{Type: "text", Text: textBuf.String()})
	}
	for _, idx := range toolOrder {
		acc := tools_[idx]
		var input map[string]any
		if acc.argsBuf.Len() > 0 {
			json.Unmarshal([]byte(acc.argsBuf.String()), &input) //nolint:errcheck
		}
		out.Content = append(out.Content, ContentBlock{
			Type:  "tool_use",
			ID:    acc.id,
			Name:  acc.name,
			Input: input,
		})
	}

	if cb != nil {
		cb("message_stop", map[string]any{})
	}

	// OpenAI streaming does not return usage in the main stream by default.
	// Usage stays zero; callers relying on LastUsage() will see the last Claude value.
	if ctx.Err() != nil {
		return out, Usage{}, ctx.Err()
	}
	return out, Usage{}, nil
}

func readOAIError(body []byte) string {
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &e) == nil && e.Error.Message != "" {
		return e.Error.Message
	}
	return string(body)
}

// ── Constructor + named providers ─────────────────────────────────────────────

// New returns an OpenAI-compatible Provider with an explicit API key.
func New(baseURL, apiKey, defaultModel string) Provider {
	return openaiProvider{baseURL: baseURL, apiKey: apiKey, defaultModel: defaultModel}
}

// NewFromEnv returns an OpenAI-compatible Provider that reads its API key from
// envVar at call time, so keys set after process start are picked up.
func NewFromEnv(baseURL, envVar, defaultModel string) Provider {
	return openaiProvider{baseURL: baseURL, envKeyName: envVar, defaultModel: defaultModel}
}

// ProviderEnvVar returns the env var name for a named provider ("kimi", "minimax").
func ProviderEnvVar(name string) string {
	switch name {
	case "kimi":
		return "MOONSHOT_API_KEY"
	case "minimax":
		return "MINIMAX_API_KEY"
	default:
		return ""
	}
}

// Kimi is the Moonshot AI provider (openai-compatible).
// Requires MOONSHOT_API_KEY to be set in the environment.
var Kimi = NewFromEnv("https://api.moonshot.cn/v1", "MOONSHOT_API_KEY", "moonshot-v1-8k")

// MiniMax is the MiniMax provider (openai-compatible).
// Requires MINIMAX_API_KEY to be set in the environment.
var MiniMax Provider = NewClaudeCompatible("https://api.minimax.io/anthropic/v1/messages", "MINIMAX_API_KEY", "MiniMax-M3")
