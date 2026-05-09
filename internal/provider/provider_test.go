package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// overrideBase points apiBase at ts for the duration of the test.
func overrideBase(t *testing.T, ts *httptest.Server) {
	t.Helper()
	orig := apiBase
	apiBase = ts.URL
	t.Cleanup(func() { apiBase = orig })
}

// checkCacheControl asserts that a decoded JSON object has cache_control.type == "ephemeral".
func checkCacheControl(t *testing.T, label string, obj map[string]any) {
	t.Helper()
	cc, ok := obj["cache_control"].(map[string]any)
	if !ok {
		t.Errorf("%s: missing cache_control", label)
		return
	}
	if cc["type"] != "ephemeral" {
		t.Errorf("%s: cache_control.type = %v, want ephemeral", label, cc["type"])
	}
}

// mustNoCC asserts that a decoded JSON object does NOT have cache_control.
func mustNoCC(t *testing.T, label string, obj map[string]any) {
	t.Helper()
	if _, ok := obj["cache_control"]; ok {
		t.Errorf("%s: should not have cache_control", label)
	}
}

// verifyCaching inspects the decoded request body for correct cache_control placement.
func verifyCaching(t *testing.T, body map[string]any) {
	t.Helper()

	// System: last block is ephemeral.
	sys := mustSlice(t, body, "system")
	checkCacheControl(t, "system[-1]", mustObj(t, sys[len(sys)-1]))

	// Tools: only the last tool is ephemeral.
	tools := mustSlice(t, body, "tools")
	for i, raw := range tools {
		obj := mustObj(t, raw)
		if i < len(tools)-1 {
			mustNoCC(t, fmt.Sprintf("tools[%d]", i), obj)
		} else {
			checkCacheControl(t, "tools[-1]", obj)
		}
	}

	// Messages: only the last block of the last message is ephemeral.
	msgs := mustSlice(t, body, "messages")
	for mi, rawMsg := range msgs {
		msgObj := mustObj(t, rawMsg)
		content := mustSlice(t, msgObj, "content")
		for ci, rawBlk := range content {
			blk := mustObj(t, rawBlk)
			isLastMsg := mi == len(msgs)-1
			isLastBlk := ci == len(content)-1
			if isLastMsg && isLastBlk {
				checkCacheControl(t, fmt.Sprintf("messages[%d].content[%d]", mi, ci), blk)
			} else {
				mustNoCC(t, fmt.Sprintf("messages[%d].content[%d]", mi, ci), blk)
			}
		}
	}
}

func mustSlice(t *testing.T, m map[string]any, key string) []any {
	t.Helper()
	v, ok := m[key].([]any)
	if !ok {
		t.Fatalf("expected %q to be []any, got %T", key, m[key])
	}
	return v
}

func mustObj(t *testing.T, v any) map[string]any {
	t.Helper()
	obj, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", v)
	}
	return obj
}

// testInput returns a canonical set of messages + tools used across tests.
func testInput() ([]Message, []ToolSchema) {
	messages := []Message{
		{Role: "user", Content: []ContentBlock{
			{Type: "text", Text: "first turn"},
		}},
		{Role: "assistant", Content: []ContentBlock{
			{Type: "text", Text: "first reply"},
		}},
		{Role: "user", Content: []ContentBlock{
			{Type: "text", Text: "line A"},
			{Type: "text", Text: "line B"}, // last block — gets cache_control
		}},
	}
	tools := []ToolSchema{
		{Name: "tool_alpha", Description: "first tool", InputSchema: map[string]any{"type": "object"}},
		{Name: "tool_beta", Description: "last tool", InputSchema: map[string]any{"type": "object"}},
	}
	return messages, tools
}

// ── TestCallClaude ────────────────────────────────────────────────────────────

func TestCallClaude(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key-sync")

	var (
		gotHeaders http.Header
		gotBody    map[string]any
	)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"role": "assistant",
			"content": []any{
				map[string]any{"type": "text", "text": "Hi there!"},
				map[string]any{
					"type":  "tool_use",
					"id":    "toolu_01",
					"name":  "tool_beta",
					"input": map[string]any{"param": "value"},
				},
			},
			"usage": map[string]any{
				"input_tokens":                   25,
				"output_tokens":                  10,
				"cache_creation_input_tokens":    100,
				"cache_read_input_tokens":        50,
			},
		})
	}))
	defer ts.Close()
	overrideBase(t, ts)

	messages, tools := testInput()
	msg, usage, err := CallClaude(context.Background(), messages, "you are helpful", tools, "claude-opus-4-7")
	if err != nil {
		t.Fatalf("CallClaude: %v", err)
	}

	// ── Headers ──────────────────────────────────────────────────────────────
	for _, tc := range []struct{ key, want string }{
		{"x-api-key", "test-key-sync"},
		{"anthropic-version", "2023-06-01"},
		{"content-type", "application/json"},
		{"anthropic-beta", "prompt-caching-2024-07-31"},
	} {
		if got := gotHeaders.Get(tc.key); got != tc.want {
			t.Errorf("header %q: got %q, want %q", tc.key, got, tc.want)
		}
	}

	// ── Caching markers ───────────────────────────────────────────────────────
	verifyCaching(t, gotBody)

	// ── max_tokens propagated ────────────────────────────────────────────────
	if mt, _ := gotBody["max_tokens"].(float64); int(mt) != maxTokens {
		t.Errorf("max_tokens: got %v, want %d", gotBody["max_tokens"], maxTokens)
	}

	// ── Response parsing ──────────────────────────────────────────────────────
	if msg.Role != "assistant" {
		t.Errorf("role: got %q, want assistant", msg.Role)
	}
	if len(msg.Content) != 2 {
		t.Fatalf("content len: got %d, want 2", len(msg.Content))
	}
	if msg.Content[0].Type != "text" || msg.Content[0].Text != "Hi there!" {
		t.Errorf("content[0]: %+v", msg.Content[0])
	}
	if msg.Content[1].Type != "tool_use" || msg.Content[1].Name != "tool_beta" {
		t.Errorf("content[1]: %+v", msg.Content[1])
	}
	if msg.Content[1].Input["param"] != "value" {
		t.Errorf("tool input: %+v", msg.Content[1].Input)
	}

	// ── Usage ─────────────────────────────────────────────────────────────────
	if usage.InputTokens != 25 || usage.OutputTokens != 10 {
		t.Errorf("token usage: %+v", usage)
	}
	if usage.CacheWrite != 100 || usage.CacheRead != 50 {
		t.Errorf("cache usage: %+v", usage)
	}

	// ── LastUsage reflects the call ───────────────────────────────────────────
	if last := LastUsage(); last != usage {
		t.Errorf("LastUsage: got %+v, want %+v", last, usage)
	}
}

// ── TestCallClaudeStreaming ───────────────────────────────────────────────────

// sseEvent formats a single SSE data line.
func sseEvent(obj any) string {
	b, _ := json.Marshal(obj)
	return "data: " + string(b) + "\n\n"
}

func TestCallClaudeStreaming(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key-stream")

	var (
		gotHeaders http.Header
		gotBody    map[string]any
	)

	// Build the SSE stream as a string: text block + tool_use block.
	var sb strings.Builder
	for _, evt := range []any{
		map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"role": "assistant", "content": []any{},
				"usage": map[string]any{
					"input_tokens":                   20,
					"output_tokens":                  0,
					"cache_creation_input_tokens":    80,
					"cache_read_input_tokens":        40,
				},
			},
		},
		map[string]any{"type": "content_block_start", "index": 0,
			"content_block": map[string]any{"type": "text", "text": ""}},
		map[string]any{"type": "content_block_delta", "index": 0,
			"delta": map[string]any{"type": "text_delta", "text": "Hello "}},
		map[string]any{"type": "content_block_delta", "index": 0,
			"delta": map[string]any{"type": "text_delta", "text": "world"}},
		map[string]any{"type": "content_block_stop", "index": 0},
		map[string]any{"type": "content_block_start", "index": 1,
			"content_block": map[string]any{"type": "tool_use", "id": "t01", "name": "tool_beta", "input": map[string]any{}}},
		map[string]any{"type": "content_block_delta", "index": 1,
			"delta": map[string]any{"type": "input_json_delta", "partial_json": `{"key":`}},
		map[string]any{"type": "content_block_delta", "index": 1,
			"delta": map[string]any{"type": "input_json_delta", "partial_json": `"val"}`}},
		map[string]any{"type": "content_block_stop", "index": 1},
		map[string]any{"type": "message_delta",
			"delta":  map[string]any{"stop_reason": "tool_use"},
			"usage":  map[string]any{"output_tokens": 18}},
		map[string]any{"type": "message_stop"},
	} {
		sb.WriteString(sseEvent(evt))
	}
	stream := sb.String()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, stream)
	}))
	defer ts.Close()
	overrideBase(t, ts)

	messages, tools := testInput()

	type cbEvent struct {
		evtType string
		data    map[string]any
	}
	var cbEvents []cbEvent
	cb := StreamCallback(func(evtType string, data map[string]any) {
		cbEvents = append(cbEvents, cbEvent{evtType, data})
	})

	msg, usage, err := CallClaudeStreaming(context.Background(), messages, "you are helpful", tools, "claude-opus-4-7", cb)
	if err != nil {
		t.Fatalf("CallClaudeStreaming: %v", err)
	}

	// ── Headers ───────────────────────────────────────────────────────────────
	for _, tc := range []struct{ key, want string }{
		{"x-api-key", "test-key-stream"},
		{"anthropic-version", "2023-06-01"},
		{"content-type", "application/json"},
		{"anthropic-beta", "prompt-caching-2024-07-31"},
	} {
		if got := gotHeaders.Get(tc.key); got != tc.want {
			t.Errorf("header %q: got %q, want %q", tc.key, got, tc.want)
		}
	}

	// ── Caching markers ───────────────────────────────────────────────────────
	verifyCaching(t, gotBody)

	// ── Assembled message ─────────────────────────────────────────────────────
	if msg.Role != "assistant" {
		t.Errorf("role: got %q", msg.Role)
	}
	if len(msg.Content) != 2 {
		t.Fatalf("content len: got %d, want 2", len(msg.Content))
	}
	if msg.Content[0].Type != "text" || msg.Content[0].Text != "Hello world" {
		t.Errorf("text block: %+v", msg.Content[0])
	}
	if msg.Content[1].Type != "tool_use" || msg.Content[1].ID != "t01" || msg.Content[1].Name != "tool_beta" {
		t.Errorf("tool block: %+v", msg.Content[1])
	}
	if msg.Content[1].Input["key"] != "val" {
		t.Errorf("tool input: %+v", msg.Content[1].Input)
	}

	// ── Streaming usage ───────────────────────────────────────────────────────
	if usage.InputTokens != 20 || usage.OutputTokens != 18 {
		t.Errorf("token usage: %+v", usage)
	}
	if usage.CacheWrite != 80 || usage.CacheRead != 40 {
		t.Errorf("cache usage: %+v", usage)
	}

	// ── Callback events ───────────────────────────────────────────────────────
	// Collect event types for inspection.
	evtTypes := make([]string, len(cbEvents))
	for i, e := range cbEvents {
		evtTypes[i] = e.evtType
	}

	// Must contain TextStart, two TextDeltas, ToolStart, ToolComplete, message_stop.
	mustContain := []string{TextStart, TextDelta, ToolStart, ToolComplete, "message_stop"}
	for _, want := range mustContain {
		found := false
		for _, got := range evtTypes {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("callback: missing event %q; got %v", want, evtTypes)
		}
	}

	// ToolComplete must carry the assembled input.
	for _, e := range cbEvents {
		if e.evtType == ToolComplete {
			input, _ := e.data["input"].(map[string]any)
			if input["key"] != "val" {
				t.Errorf("ToolComplete input: %+v", e.data["input"])
			}
		}
	}

	// TextDelta events must carry non-empty text.
	for _, e := range cbEvents {
		if e.evtType == TextDelta {
			text, _ := e.data["text"].(string)
			if text == "" {
				t.Errorf("TextDelta with empty text")
			}
		}
	}
}
