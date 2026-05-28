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
	"strings"
	"syscall"
	"time"

	"github.com/trymimicode/mimicode-go/internal/agent"
	"github.com/trymimicode/mimicode-go/internal/compactor"
	"github.com/trymimicode/mimicode-go/internal/provider"
	reflectpkg "github.com/trymimicode/mimicode-go/internal/reflect"
	"github.com/trymimicode/mimicode-go/internal/repomap"
	"github.com/trymimicode/mimicode-go/internal/store"
)

const version = "dev"

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
	fs := flag.NewFlagSet("mimicode", flag.ContinueOnError)
	fs.SetOutput(errOut)

	var sessionID string
	var showVersion bool
	var useTUI bool
	fs.StringVar(&sessionID, "s", "", "named session id")
	fs.BoolVar(&showVersion, "version", false, "print version and exit")
	fs.BoolVar(&useTUI, "tui", false, "start terminal UI")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if showVersion {
		fmt.Fprintln(out, version)
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

	prompt := strings.Join(fs.Args(), " ")
	if prompt != "" {
		return runOneShot(ctx, sessionID, cwd, prompt, out, errOut)
	}
	return runREPL(ctx, sessionID, cwd, in, out, errOut)
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

func runOneShot(ctx context.Context, sessionID, cwd, prompt string, out, errOut io.Writer) int {
	sess, messages, ok := startSession(sessionID, cwd, errOut)
	if !ok {
		return 1
	}

	printTurnStart(errOut, sess)
	cfg := agent.AgentConfig{CWD: cwd, Session: sess, MaxSteps: 25}
	var err error
	messages, err = agentTurn(ctx, cfg, prompt, messages)
	if err != nil {
		printAgentErr(errOut, err)
		return 1
	}
	if err := sess.SaveMessages(messages); err != nil {
		fmt.Fprintf(errOut, "mimicode: save messages: %v\n", err)
		return 1
	}
	messages = maybeCompactAndSave(ctx, sess, messages, errOut)

	if text := extractLastAssistantText(messages); text != "" {
		fmt.Fprintln(out, text)
	}
	reflectSession(ctx, sess, cwd)
	return 0
}

func runREPL(ctx context.Context, sessionID, cwd string, in io.Reader, out, errOut io.Writer) int {
	sess, messages, ok := startSession(sessionID, cwd, errOut)
	if !ok {
		return 1
	}
	cfg := agent.AgentConfig{CWD: cwd, Session: sess, MaxSteps: 25}
	fmt.Fprintln(errOut, "[mimicode] REPL. empty line or :q / ctrl-d to exit. :compact for compaction.")

	reader := bufio.NewReader(in)
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

		printTurnStart(errOut, sess)
		var turnErr error
		messages, turnErr = agentTurn(ctx, cfg, prompt, messages)
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
		if text := extractLastAssistantText(messages); text != "" {
			fmt.Fprintln(out, text)
			fmt.Fprintln(out)
		}
		if err == io.EOF {
			break
		}
	}

	reflectSession(ctx, sess, cwd)
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

func printAgentErr(errOut io.Writer, err error) {
	if agent.IsInterrupted(err) {
		fmt.Fprintln(errOut, "mimicode: interrupted")
		return
	}
	fmt.Fprintf(errOut, "mimicode: agent: %v\n", err)
}

func reflectSession(ctx context.Context, sess *store.Session, cwd string) {
	reflectCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
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
