package main

import (
	"context"
	"flag"
	"fmt"
	"io"

	reflectpkg "github.com/trymimicode/mimicode-go/internal/reflect"
	"github.com/trymimicode/mimicode-go/internal/watch"
)

// runWatch starts the code.mimi file watcher.
// By default it runs the local-gather (no-LLM) briefer so the command works
// without an API key.  Pass -agent to use the full LLM agent.
func runWatch(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("mimicode watch", flag.ContinueOnError)
	fs.SetOutput(errOut)

	var (
		notebook  string
		sessionID string
		useAgent  bool
	)
	fs.StringVar(&notebook, "notebook", "", "path to the notebook (default <dir>/code.mimi)")
	fs.StringVar(&sessionID, "s", "", "named session id (persists conversation across restarts)")
	fs.BoolVar(&useAgent, "agent", true, "use LLM agent for responses (default: true)")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	dir := fs.Arg(0)
	if dir == "" {
		var err error
		if dir, err = getwd(); err != nil {
			fmt.Fprintf(errOut, "mimicode: get cwd: %v\n", err)
			return 1
		}
	}

	if _, err := lookPath("rg"); err != nil {
		fmt.Fprintln(errOut, rgInstallInstructions())
		return 1
	}

	cfg := watch.Config{
		Dir:          dir,
		NotebookPath: notebook,
		Out:          errOut,
	}

	if useAgent {
		if err := startupChecks(errOut); err != nil {
			return 1
		}
		briefer, sess, err := watch.NewAgentBriefer(sessionID, dir)
		if err != nil {
			fmt.Fprintf(errOut, "mimicode: watch: start session: %v\n", err)
			return 1
		}
		// Reflect on session when the watcher exits.
		defer func() {
			rctx, cancel := context.WithTimeout(context.Background(), 60*1e9)
			defer cancel()
			fmt.Fprintln(errOut, "[mimi] reflecting on session…")
			_ = reflectpkg.RunReflect(rctx, sess, dir)
		}()
		cfg.Brief = briefer
	}

	if err := watch.Run(ctx, cfg); err != nil {
		fmt.Fprintf(errOut, "mimicode: watch: %v\n", err)
		return 1
	}
	return 0
}
