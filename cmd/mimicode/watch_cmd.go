package main

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/trymimicode/mimicode-go/internal/watch"
)

// runWatch starts mimi as an ambient assistant in a folder. It watches a
// think-out-loud notebook and surfaces material to learn from; it does not
// write code. The watcher+inject seams (watch.Config.Brief / .Inject) are the
// integration points for the watcher loop.
func runWatch(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("mimicode watch", flag.ContinueOnError)
	fs.SetOutput(errOut)
	var notebook string
	fs.StringVar(&notebook, "notebook", "", "path to the think-out-loud notebook (default <dir>/code.mimi)")
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
	if _, err := lookPath("git"); err != nil {
		fmt.Fprintln(errOut, "mimicode: git not found; git_source/source fetch will not work")
	}

	if err := watch.Run(ctx, watch.Config{Dir: dir, NotebookPath: notebook, Out: errOut}); err != nil {
		fmt.Fprintf(errOut, "mimicode: watch: %v\n", err)
		return 1
	}
	return 0
}
