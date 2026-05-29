package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/trymimicode/mimicode-go/internal/agent"
	"github.com/trymimicode/mimicode-go/internal/checkpoint"
	"github.com/trymimicode/mimicode-go/internal/compactor"
	"github.com/trymimicode/mimicode-go/internal/memory"
	"github.com/trymimicode/mimicode-go/internal/provider"
	"github.com/trymimicode/mimicode-go/internal/recovery"
	reflectpkg "github.com/trymimicode/mimicode-go/internal/reflect"
	"github.com/trymimicode/mimicode-go/internal/repomap"
	"github.com/trymimicode/mimicode-go/internal/store"
)

var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

var (
	lookPath     = exec.LookPath
	getenv       = os.Getenv
	getwd        = os.Getwd
	agentTurn    = agent.AgentTurn
	maybeCompact = compactor.MaybeCompact
	compactNow   = compactor.Compact
	statusText   = compactor.StatusText
	runReflect   = func(ctx context.Context, sess *store.Session, cwd string) error {
		return reflectpkg.RunReflect(ctx, sess, cwd)
	}
	lastUsage = provider.LastUsage
	setenv    = os.Setenv
	runTUIApp = runTUI
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	done := make(chan struct{})
	go func() {
		<-sigCh
		fmt.Fprintln(os.Stderr)
		cancel()
		close(done)
	}()

	code := runCLI(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	cancel()
	signal.Stop(sigCh)
	select {
	case <-done:
	default:
	}
	os.Exit(code)
}

func runCLI(ctx context.Context, args []string, in io.Reader, out, errOut io.Writer) int {
	if len(args) > 0 && args[0] == "watch" {
		return runWatch(ctx, args[1:], out, errOut)
	}

	fs := flag.NewFlagSet("mimicode", flag.ContinueOnError)
	fs.SetOutput(errOut)

	var sessionID string
	var showVersion bool
	var useTUI bool
	var confirm bool
	fs.StringVar(&sessionID, "s", "", "named session id")
	fs.BoolVar(&showVersion, "version", false, "print version and exit")
	fs.BoolVar(&useTUI, "tui", false, "start terminal UI")
	fs.BoolVar(&confirm, "confirm", false, "ask before each bash/write/edit")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if showVersion {
		fmt.Fprintf(out, "mimicode version %s\n", version)
		fmt.Fprintf(out, "  commit: %s\n", commit)
		fmt.Fprintf(out, "  built:  %s\n", buildDate)
		fmt.Fprintf(out, "  go:     %s\n", runtime.Version())
		return 0
	}

	if err := startupChecks(errOut); err != nil {
		return 1
	}
	if useTUI {
		if err := runTUIApp(sessionID); err != nil {
			fmt.Fprintf(errOut, "mimicode: tui: %v\n", err)
			return 1
		}
		return 0
	}

	cwd, err := getwd()
	if err != nil {
		fmt.Fprintf(errOut, "mimicode: get cwd: %v\n", err)
		return 1
	}

	confirm = confirm || getenv("MIMICODE_CONFIRM") == "1"
	prompt := strings.Join(fs.Args(), " ")
	if prompt != "" {
		return runOneShot(ctx, sessionID, cwd, prompt, in, out, errOut, confirm)
	}
	return runREPL(ctx, sessionID, cwd, in, out, errOut, confirm)
}

func startupChecks(errOut io.Writer) error {
	if _, err := lookPath("rg"); err != nil {
		fmt.Fprintln(errOut, rgInstallInstructions())
		return err
	}
	if strings.TrimSpace(getenv("ANTHROPIC_API_KEY")) == "" {
		fmt.Fprintln(errOut, "mimicode: ANTHROPIC_API_KEY is not set")
		return fmt.Errorf("missing ANTHROPIC_API_KEY")
	}
	return nil
}

func runOneShot(ctx context.Context, sessionID, cwd, prompt string, in io.Reader, out, errOut io.Writer, confirm bool) int {
	sess, messages, ok := startSession(sessionID, cwd, errOut)
	if !ok {
		return 1
	}
	defer reflectSession(sess, cwd, errOut)

	cp := checkpoint.New(sess.Path(), cwd)
	cp.Snapshot("session start")

	printTurnStart(errOut, sess)
	cfg := agent.AgentConfig{CWD: cwd, Session: sess, MaxSteps: 25}
	if confirm {
		cfg.ConfirmTool = makeConfirmTool(bufio.NewReader(in), errOut)
	}
	var err error
	messages, err = agentTurn(ctx, cfg, prompt, messages)
	if stuck, ok := agent.IsStuck(err); ok {
		_ = sess.SaveMessages(messages)
		if diag, derr := recovery.Diagnose(ctx, sess, stuck.Reason); derr == nil {
			fmt.Fprint(errOut, diag.Format())
			fmt.Fprintln(errOut, "  (one-shot: not auto-applied — rerun in REPL to recover)")
		} else {
			fmt.Fprintf(errOut, "mimicode: stuck: %s\n", stuck.Reason)
		}
		return 1
	}
	if err != nil {
		printAgentErr(errOut, err)
		return 1
	}
	if err := sess.SaveMessages(messages); err != nil {
		fmt.Fprintf(errOut, "mimicode: save messages: %v\n", err)
		return 1
	}
	messages = maybeCompactAndSave(ctx, sess, messages, errOut)
	snapshotTurn(cp, 1, prompt, errOut)

	if text := extractLastAssistantText(messages); text != "" {
		fmt.Fprintln(out, text)
	}
	return 0
}

func runREPL(ctx context.Context, sessionID, cwd string, in io.Reader, out, errOut io.Writer, confirm bool) int {
	sess, messages, ok := startSession(sessionID, cwd, errOut)
	if !ok {
		return 1
	}
	defer reflectSession(sess, cwd, errOut)
	cfg := agent.AgentConfig{CWD: cwd, Session: sess, MaxSteps: 25}
	cp := checkpoint.New(sess.Path(), cwd)
	cp.Snapshot("session start")
	fmt.Fprintln(errOut, "[mimicode] REPL. empty line or :q / ctrl-d to exit. :compact compaction, :undo [n] revert turns.")

	turn := 0
	reader := bufio.NewReader(in)
	if confirm {
		cfg.ConfirmTool = makeConfirmTool(reader, errOut)
	}
	for {
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			fmt.Fprintf(errOut, "mimicode: read prompt: %v\n", err)
			return 1
		}
		prompt := strings.TrimSpace(line)
		if err == io.EOF && prompt == "" {
			break
		}
		if prompt == "" || prompt == ":q" || prompt == ":quit" || prompt == ":exit" {
			break
		}
		if strings.HasPrefix(prompt, ":compact") {
			messages = handleCompactCommand(ctx, sess, messages, strings.TrimSpace(strings.TrimPrefix(prompt, ":compact")), errOut)
			if err == io.EOF {
				break
			}
			continue
		}
		if strings.HasPrefix(prompt, ":undo") {
			handleUndoCommand(cp, strings.TrimSpace(strings.TrimPrefix(prompt, ":undo")), errOut)
			if err == io.EOF {
				break
			}
			continue
		}

		printTurnStart(errOut, sess)
		before := append([]provider.Message(nil), messages...)
		var turnErr error
		messages, turnErr = agentTurn(ctx, cfg, prompt, messages)
		if stuck, ok := agent.IsStuck(turnErr); ok {
			recoveryPrompt, apply := proposeRecovery(ctx, reader, sess, cwd, cp, prompt, stuck, errOut)
			if !apply {
				_ = sess.SaveMessages(messages)
				continue
			}
			messages = before // clean context: drop the failed turn, retry fresh
			messages, turnErr = agentTurn(ctx, cfg, recoveryPrompt, messages)
			if stuck2, ok := agent.IsStuck(turnErr); ok {
				fmt.Fprintf(errOut, "recovery attempt still stuck: %s\n", stuck2.Reason)
				_ = sess.SaveMessages(messages)
				continue
			}
		}
		if turnErr != nil {
			printAgentErr(errOut, turnErr)
			if agent.IsInterrupted(turnErr) {
				break
			}
			return 1
		}
		if saveErr := sess.SaveMessages(messages); saveErr != nil {
			fmt.Fprintf(errOut, "mimicode: save messages: %v\n", saveErr)
			return 1
		}
		messages = maybeCompactAndSave(ctx, sess, messages, errOut)
		turn++
		snapshotTurn(cp, turn, prompt, errOut)
		if text := extractLastAssistantText(messages); text != "" {
			fmt.Fprintln(out, text)
			fmt.Fprintln(out)
		}
		if err == io.EOF {
			break
		}
	}

	return 0
}

func startSession(sessionID, cwd string, errOut io.Writer) (*store.Session, []provider.Message, bool) {
	sess, messages, err := store.ResumeOrNew(sessionID, cwd, provider.DefaultModel())
	if err != nil {
		fmt.Fprintf(errOut, "mimicode: start session: %v\n", err)
		return nil, nil, false
	}
	if len(messages) > 0 {
		fmt.Fprintf(errOut, "resumed %d prior messages\n", len(messages))
	}
	repomap.Init(cwd)
	return sess, messages, true
}

func maybeCompactAndSave(ctx context.Context, sess *store.Session, messages []provider.Message, errOut io.Writer) []provider.Message {
	next, record, err := maybeCompact(ctx, messages, sess.Path(), lastUsage().InputTokens)
	if err != nil {
		fmt.Fprintf(errOut, "mimicode: compact: %v\n", err)
		return messages
	}
	if record == nil {
		return messages
	}
	if err := sess.SaveMessages(next); err != nil {
		fmt.Fprintf(errOut, "mimicode: save compacted messages: %v\n", err)
		return messages
	}
	fmt.Fprintf(errOut, "compacted session: %s\n", record.ID)
	return next
}

func handleCompactCommand(ctx context.Context, sess *store.Session, messages []provider.Message, arg string, errOut io.Writer) []provider.Message {
	switch arg {
	case "on":
		_ = setenv("MIMICODE_COMPACT_AUTO", "1")
		fmt.Fprintln(errOut, statusText(sess.Path(), lastUsage().InputTokens))
		return messages
	case "off":
		_ = setenv("MIMICODE_COMPACT_AUTO", "0")
		fmt.Fprintln(errOut, statusText(sess.Path(), lastUsage().InputTokens))
		return messages
	case "status":
		fmt.Fprintln(errOut, statusText(sess.Path(), lastUsage().InputTokens))
		return messages
	case "":
		next, record, err := compactNow(ctx, messages, sess.Path(), 3, "manual")
		if err != nil {
			fmt.Fprintf(errOut, "mimicode: compact: %v\n", err)
			return messages
		}
		if record == nil {
			fmt.Fprintln(errOut, "mimicode: nothing to compact")
			return messages
		}
		if err := sess.SaveMessages(next); err != nil {
			fmt.Fprintf(errOut, "mimicode: save compacted messages: %v\n", err)
			return messages
		}
		fmt.Fprintf(errOut, "compacted session: %s\n", record.ID)
		return next
	default:
		fmt.Fprintln(errOut, "usage: :compact [on|off|status]")
		return messages
	}
}

func printTurnStart(errOut io.Writer, sess *store.Session) {
	fmt.Fprintf(errOut, "session: %s  model: %s\n", sess.ID, sess.Model)
}

func snapshotTurn(cp *checkpoint.Checkpointer, turn int, prompt string, errOut io.Writer) {
	label := fmt.Sprintf("turn %d: %s", turn, firstLine(prompt, 60))
	sha, err := cp.Snapshot(label)
	if err != nil {
		fmt.Fprintf(errOut, "checkpoint: %v\n", err)
		return
	}
	if sha != "" {
		fmt.Fprintf(errOut, "checkpoint %s — %s\n", sha, label)
	}
}

func handleUndoCommand(cp *checkpoint.Checkpointer, arg string, errOut io.Writer) {
	if arg == "list" {
		entries := cp.List()
		if len(entries) == 0 {
			fmt.Fprintln(errOut, "no checkpoints")
			return
		}
		for _, e := range entries {
			fmt.Fprintf(errOut, "  %s  %s\n", e.SHA, e.Label)
		}
		return
	}
	n := 1
	if arg != "" {
		parsed, err := strconv.Atoi(arg)
		if err != nil || parsed < 1 {
			fmt.Fprintln(errOut, "usage: :undo [n|list]")
			return
		}
		n = parsed
	}
	label, err := cp.Undo(n)
	if err != nil {
		fmt.Fprintf(errOut, "undo: %v\n", err)
		return
	}
	fmt.Fprintf(errOut, "reverted to: %s\n", label)
}

func firstLine(s string, max int) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > max {
		s = s[:max] + "…"
	}
	return s
}

// proposeRecovery diagnoses a stuck turn from the event log, shows it, and asks
// the engineer whether to apply. Returns the recovery prompt and whether to retry.
func proposeRecovery(ctx context.Context, reader *bufio.Reader, sess *store.Session, cwd string, cp *checkpoint.Checkpointer, originalPrompt string, stuck agent.AgentStuck, errOut io.Writer) (string, bool) {
	diag, err := recovery.Diagnose(ctx, sess, stuck.Reason)
	if err != nil {
		fmt.Fprintf(errOut, "recovery: diagnosis failed: %v\n", err)
		return "", false
	}
	fmt.Fprint(errOut, diag.Format())
	fmt.Fprint(errOut, "  apply recovery? [y]es retry / [r]ule only / [n]o: ")

	line, _ := reader.ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		if diag.Rule != "" {
			if err := memory.AppendRule(cwd, diag.Rule); err != nil {
				fmt.Fprintf(errOut, "  warning: could not write rule: %v\n", err)
			} else {
				fmt.Fprintln(errOut, "  rule added to .mimi/RULES.md")
			}
		}
		cp.Snapshot("before recovery retry")
		fmt.Fprintln(errOut, "  resetting to a clean context and retrying…")
		return buildRecoveryPrompt(originalPrompt, diag), true
	case "r", "rule":
		if diag.Rule != "" {
			if err := memory.AppendRule(cwd, diag.Rule); err != nil {
				fmt.Fprintf(errOut, "  warning: could not write rule: %v\n", err)
			} else {
				fmt.Fprintln(errOut, "  rule added to .mimi/RULES.md (no retry)")
			}
		}
		return "", false
	default:
		fmt.Fprintln(errOut, "  recovery skipped")
		return "", false
	}
}

// makeConfirmTool returns a gate that shows a pending bash/write/edit call and
// reads y/n from the shared input reader.
func makeConfirmTool(reader *bufio.Reader, w io.Writer) func(string, map[string]any) bool {
	return func(name string, input map[string]any) bool {
		fmt.Fprintf(w, "\n  mimi wants to run %s:\n%s", name, renderToolForConfirm(name, input))
		fmt.Fprint(w, "  allow? [y]es / [n]o: ")
		line, _ := reader.ReadString('\n')
		ans := strings.ToLower(strings.TrimSpace(line))
		return ans == "y" || ans == "yes"
	}
}

func renderToolForConfirm(name string, input map[string]any) string {
	switch name {
	case "bash":
		return "    $ " + asString(input["cmd"]) + "\n"
	case "write":
		content := asString(input["content"])
		return fmt.Sprintf("    %s (%d lines)\n", asString(input["path"]), strings.Count(content, "\n")+1)
	case "edit":
		path := asString(input["path"])
		if edits, ok := input["edits"].([]any); ok && len(edits) > 0 {
			return fmt.Sprintf("    %s (%d edits)\n", path, len(edits))
		}
		return fmt.Sprintf("    %s\n    - %s\n    + %s\n", path,
			firstLine(asString(input["old_text"]), 60), firstLine(asString(input["new_text"]), 60))
	default:
		return fmt.Sprintf("    %v\n", input)
	}
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func buildRecoveryPrompt(originalPrompt string, diag recovery.Diagnosis) string {
	var b strings.Builder
	b.WriteString(originalPrompt)
	fmt.Fprintf(&b, "\n\n[recovery] A previous attempt got stuck. Root cause: %s", diag.WentWrong)
	if diag.Instruction != "" {
		fmt.Fprintf(&b, " Take a different approach: %s", diag.Instruction)
	}
	return b.String()
}

func printAgentErr(errOut io.Writer, err error) {
	if agent.IsInterrupted(err) {
		fmt.Fprintln(errOut, "mimicode: interrupted")
		return
	}
	fmt.Fprintf(errOut, "mimicode: agent: %v\n", err)
}

// reflectSession runs the post-session reflection. It uses a fresh context
// (not the request ctx) so it still runs when the session was interrupted.
func reflectSession(sess *store.Session, cwd string, errOut io.Writer) {
	reflectCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	fmt.Fprintln(errOut, "reflecting on session…")
	_ = runReflect(reflectCtx, sess, cwd)
}

func extractLastAssistantText(messages []provider.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != "assistant" {
			continue
		}
		var parts []string
		for _, block := range messages[i].Content {
			if block.Type == "text" && block.Text != "" {
				parts = append(parts, block.Text)
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

func rgInstallInstructions() string {
	switch runtime.GOOS {
	case "windows":
		return "mimicode: ripgrep (rg) is required. Install it with: winget install BurntSushi.ripgrep.MSVC"
	case "darwin":
		return "mimicode: ripgrep (rg) is required. Install it with: brew install ripgrep"
	default:
		return "mimicode: ripgrep (rg) is required. Install it with your package manager, e.g. apt install ripgrep"
	}
}
