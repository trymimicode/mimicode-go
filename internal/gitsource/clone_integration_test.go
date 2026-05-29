package gitsource

import (
	"context"
	"os"
	"testing"
)

// TestCloneIntegration performs a real shallow clone. It is opt-in (needs
// network + git) to keep the default test run hermetic. Run with:
//
//	MIMICODE_NET_TEST=1 go test ./internal/gitsource/ -run Integration -v
func TestCloneIntegration(t *testing.T) {
	if os.Getenv("MIMICODE_NET_TEST") == "" {
		t.Skip("set MIMICODE_NET_TEST=1 to run the live clone test")
	}
	cwd := t.TempDir()
	ctx := context.Background()

	r, err := Clone(ctx, cwd, "octocat/Hello-World", "")
	if err != nil {
		t.Fatalf("clone: %v", err)
	}
	if r.Cached {
		t.Errorf("first clone should not be cached")
	}
	if !isGitRepo(r.LocalPath) {
		t.Fatalf("clone did not produce a git repo at %s", r.LocalPath)
	}

	// Second call reuses the cache.
	r2, err := Clone(ctx, cwd, "octocat/Hello-World", "")
	if err != nil {
		t.Fatalf("re-clone: %v", err)
	}
	if !r2.Cached {
		t.Errorf("second clone should be cached")
	}

	if repos := List(cwd); len(repos) != 1 {
		t.Errorf("List = %d repos, want 1", len(repos))
	}
	if files, total := FileList(r.LocalPath, 10); total == 0 || files == "" {
		t.Errorf("FileList returned nothing (total=%d)", total)
	}
}
