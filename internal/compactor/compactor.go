package compactor

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/trymimicode/mimicode-go/internal/provider"
)

const (
	COMPACTION_PROMPT = `You are summarizing a slice of a coding-agent transcript...
Return ONLY a single JSON object — no prose, no code fences. Schema:
{"one_line": "...", "user_intents": [...], "decisions": [...], "files_touched": [{path, what, why}], "tools_used": {bash:0,...}, "key_findings": [...], "open_issues": [...]}
If a previous "[COMPACTED ...]" marker appears in the slice, fold its contents into your output.
Slice transcript:
%s
`

	defaultAutoCompact    = true
	defaultTurnInterval   = 5
	defaultTokenThreshold = 20000
	defaultKeepRecent     = 3
	haikuModel            = "claude-haiku-4-5-20251001"
)

type CompactionRecord struct {
	ID        string         `json:"id"`
	Timestamp float64        `json:"timestamp"`
	TurnRange [2]int         `json:"turn_range"`
	MsgCount  int            `json:"msg_count"`
	Reason    string         `json:"reason"`
	Summary   map[string]any `json:"summary"`
}

var summarizeTranscript = summarize

func isRealUserTurn(m provider.Message) bool {
	return m.Role == "user" &&
		len(m.Content) == 1 &&
		m.Content[0].Type == "text"
}

func isMarker(m provider.Message) bool {
	return isRealUserTurn(m) && strings.HasPrefix(m.Content[0].Text, "[COMPACTED")
}

func findSplit(messages []provider.Message, keepRecent int) int {
	if keepRecent <= 0 {
		keepRecent = 1
	}

	var userTurns []int
	for i, msg := range messages {
		if isRealUserTurn(msg) {
			userTurns = append(userTurns, i)
		}
	}
	if len(userTurns) <= keepRecent {
		return 0
	}
	return userTurns[len(userTurns)-keepRecent]
}

func uncompactedCount(messages []provider.Message) int {
	var count int
	for _, msg := range messages {
		if isRealUserTurn(msg) && !isMarker(msg) {
			count++
		}
	}
	return count
}

func ShouldAutoCompact(messages []provider.Message, lastTokensIn int) (bool, string) {
	cfg := compactConfig()
	if !cfg.auto {
		return false, ""
	}
	if findSplit(messages, defaultKeepRecent) == 0 {
		return false, ""
	}
	if uncompactedCount(messages) >= cfg.turnInterval+defaultKeepRecent {
		return true, fmt.Sprintf("turn_interval:%d", cfg.turnInterval)
	}
	if lastTokensIn >= cfg.tokenThreshold {
		return true, fmt.Sprintf("token_threshold:%d", cfg.tokenThreshold)
	}
	return false, ""
}

func flattenForSummary(messages []provider.Message) string {
	var b strings.Builder
	for _, msg := range messages {
		for _, block := range msg.Content {
			switch block.Type {
			case "text":
				fmt.Fprintf(&b, "[%s] %s\n", msg.Role, block.Text)
			case "tool_use":
				input, _ := json.Marshal(block.Input)
				fmt.Fprintf(&b, "[%s tool_use:%s] %s\n", msg.Role, block.Name, truncate(string(input), 300))
			case "tool_result":
				fmt.Fprintf(&b, "[%s tool_result:%s] %s\n", msg.Role, block.ToolUseID, truncate(block.Content, 600))
			}
		}
	}
	return b.String()
}

func summarize(ctx context.Context, transcript string) map[string]any {
	prompt := fmt.Sprintf(COMPACTION_PROMPT, transcript)
	msg, _, err := provider.CallClaude(ctx, []provider.Message{{
		Role: "user",
		Content: []provider.ContentBlock{{
			Type: "text",
			Text: prompt,
		}},
	}}, "", nil, haikuModel)
	if err != nil || len(msg.Content) == 0 {
		return summaryParseFailed()
	}

	raw := strings.TrimSpace(msg.Content[0].Text)
	raw = strings.TrimSpace(strings.TrimPrefix(raw, "```json"))
	raw = strings.TrimSpace(strings.TrimPrefix(raw, "```"))
	raw = strings.TrimSpace(strings.TrimSuffix(raw, "```"))

	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return summaryParseFailed()
	}
	return out
}

func formatMarker(id string, summary map[string]any, turnRange [2]int) string {
	oneLine, _ := summary["one_line"].(string)
	if oneLine == "" {
		oneLine = "compacted transcript slice"
	}
	data, err := json.Marshal(summary)
	if err != nil {
		data = []byte(`{"one_line":"compacted transcript slice"}`)
	}
	return fmt.Sprintf("[COMPACTED — turns %d–%d, id=%s]\n%s\n%s", turnRange[0], turnRange[1], id, oneLine, data)
}

func Compact(ctx context.Context, messages []provider.Message, sessionPath string, keepRecent int, reason string) ([]provider.Message, *CompactionRecord, error) {
	split := findSplit(messages, keepRecent)
	if split == 0 {
		return messages, nil, nil
	}

	compacted := messages[:split]
	turnRange := [2]int{1, countRealUserTurns(compacted)}
	summary := summarizeTranscript(ctx, flattenForSummary(compacted))
	id, err := nextCompactionID(sessionPath)
	if err != nil {
		return nil, nil, err
	}

	record := &CompactionRecord{
		ID:        id,
		Timestamp: float64(time.Now().UnixNano()) / 1e9,
		TurnRange: turnRange,
		MsgCount:  len(compacted),
		Reason:    reason,
		Summary:   summary,
	}
	if err := appendCompactionRecord(sessionPath, *record); err != nil {
		return nil, nil, err
	}
	if err := appendCompactionIndex(sessionPath, *record); err != nil {
		return nil, nil, err
	}

	marker := provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{{
			Type: "text",
			Text: formatMarker(id, summary, turnRange),
		}},
	}
	ack := provider.Message{
		Role: "assistant",
		Content: []provider.ContentBlock{{
			Type: "text",
			Text: "Acknowledged. I will use the compacted transcript summary as context.",
		}},
	}

	next := make([]provider.Message, 0, 2+len(messages[split:]))
	next = append(next, marker, ack)
	next = append(next, messages[split:]...)
	return next, record, nil
}

func MaybeCompact(ctx context.Context, messages []provider.Message, sessionPath string, lastTokensIn int) ([]provider.Message, *CompactionRecord, error) {
	ok, reason := ShouldAutoCompact(messages, lastTokensIn)
	if !ok {
		return messages, nil, nil
	}
	return Compact(ctx, messages, sessionPath, defaultKeepRecent, "auto:"+reason)
}

func ListCompactions(sessionPath string) []CompactionRecord {
	f, err := os.Open(compactionsPath(sessionPath))
	if err != nil {
		return nil
	}
	defer f.Close()

	var records []CompactionRecord
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		var record CompactionRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err == nil {
			records = append(records, record)
		}
	}
	return records
}

func LoadCompaction(sessionPath, id string) *CompactionRecord {
	for _, record := range ListCompactions(sessionPath) {
		if record.ID == id {
			return &record
		}
	}
	return nil
}

func StatusText(sessionPath string, lastTokensIn int) string {
	records := ListCompactions(sessionPath)
	cfg := compactConfig()
	state := "enabled"
	if !cfg.auto {
		state = "disabled"
	}
	return fmt.Sprintf("[compactor] auto=%s compactions=%d last_tokens_in=%d threshold=%d", state, len(records), lastTokensIn, cfg.tokenThreshold)
}

type config struct {
	auto           bool
	turnInterval   int
	tokenThreshold int
}

func compactConfig() config {
	return config{
		auto:           envBool("MIMICODE_COMPACT_AUTO", defaultAutoCompact),
		turnInterval:   envInt("MIMICODE_COMPACT_TURN_INTERVAL", defaultTurnInterval),
		tokenThreshold: envInt("MIMICODE_COMPACT_TOKEN_THRESHOLD", defaultTokenThreshold),
	}
}

func envBool(key string, def bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return def
	}
	return value != "0"
}

func envInt(key string, def int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return def
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return def
	}
	return parsed
}

func summaryParseFailed() map[string]any {
	return map[string]any{"one_line": "compaction-summary-parse-failed"}
}

func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "... (truncated)"
}

func countRealUserTurns(messages []provider.Message) int {
	var count int
	for _, msg := range messages {
		if isRealUserTurn(msg) {
			count++
		}
	}
	return count
}

func nextCompactionID(sessionPath string) (string, error) {
	f, err := os.Open(compactionsPath(sessionPath))
	if os.IsNotExist(err) {
		return "c001", nil
	}
	if err != nil {
		return "", err
	}
	defer f.Close()

	var count int
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		count++
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return fmt.Sprintf("c%03d", count+1), nil
}

func appendCompactionRecord(sessionPath string, record CompactionRecord) error {
	path := compactionsPath(sessionPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(f, string(data))
	return err
}

func appendCompactionIndex(sessionPath string, record CompactionRecord) error {
	path := indexPath(sessionPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	var records []CompactionRecord
	data, err := os.ReadFile(path)
	if err == nil && len(strings.TrimSpace(string(data))) > 0 {
		if err := json.Unmarshal(data, &records); err != nil {
			return err
		}
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}

	records = append(records, record)
	data, err = json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func compactionsPath(sessionPath string) string {
	return sessionPath + ".compactions.jsonl"
}

func indexPath(sessionPath string) string {
	return sessionPath + ".compactions.index.json"
}
