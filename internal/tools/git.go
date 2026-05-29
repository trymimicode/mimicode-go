package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/trymimicode/mimicode-go/internal/gitsource"
)

// GitSource shallow-clones a real repository into the project cache and reports
// the local path plus a file listing, so the engineer (or mimi) can then rg/read
// the actual source. This is the anti-vibing tool: read how a library really
// works instead of trusting a generated guess.
func GitSource(ctx context.Context, cwd, repo, ref string) ToolResult {
	repo = strings.TrimSpace(repo)
	if repo == "" {
		return ToolResult{Output: "git_source: repo is required (e.g. \"jackc/pgx\" or a full git URL)", IsError: true}
	}

	r, err := gitsource.Clone(ctx, cwd, repo, ref)
	if err != nil {
		return ToolResult{Output: "[git_source error] " + err.Error(), IsError: true}
	}

	files, total := gitsource.FileList(r.LocalPath, 80)
	status := "cloned"
	if r.Cached {
		status = "cached"
	}
	refLabel := r.Ref
	if refLabel == "" {
		refLabel = "(default branch)"
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "%s %s\n  ref:        %s\n  local path: %s\n\n", status, r.URL, refLabel, r.LocalPath)
	sb.WriteString("Explore the real source with rg/read on that path — e.g. `rg -n 'func New' " + r.LocalPath + "`\n\n")
	if total == 0 {
		sb.WriteString("(could not list files)")
	} else {
		fmt.Fprintf(&sb, "files (%d total, showing first %d):\n%s", total, min(total, 80), files)
	}
	return ToolResult{Output: sb.String()}
}
