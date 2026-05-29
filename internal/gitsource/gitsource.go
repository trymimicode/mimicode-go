// Package gitsource fetches real source code from git remotes so the engineer
// can read how a library actually works — not how a probabilistic model guesses
// it works. Repos are shallow-cloned into <cwd>/.mimi/cache/<host>/<owner>/<repo>
// and then explored with the normal rg/read tools, so call sites can be followed
// through the real tree. This is the anti-vibing primitive: learn from the truth.
package gitsource

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// Repo describes a cached, shallow-cloned source repository.
type Repo struct {
	URL       string // normalized https clone URL
	Ref       string // branch/tag requested, or "" for the remote default
	LocalPath string // absolute path of the clone under .mimi/cache
	Cached    bool   // true if the clone already existed (no fresh fetch)
}

var (
	schemeRE = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.\-]*://`)
	scpRE    = regexp.MustCompile(`^(?:[^@/]+@)?([^:/]+):(.+)$`)
	unsafeRE = regexp.MustCompile(`[^a-z0-9._-]+`)
)

// CacheRoot returns the directory under cwd where clones are cached.
func CacheRoot(cwd string) string {
	return filepath.Join(cwd, ".mimi", "cache")
}

// Clone shallow-clones raw (a repo reference) into the cache under cwd and
// returns the Repo. If the repo is already cached it is reused as-is. ref, if
// non-empty, selects a branch or tag. Accepts shorthand ("owner/repo"),
// host paths ("github.com/owner/repo"), full URLs, and scp form
// ("git@github.com:owner/repo.git").
func Clone(ctx context.Context, cwd, raw, ref string) (*Repo, error) {
	cloneURL, slug, err := normalize(raw)
	if err != nil {
		return nil, err
	}

	dest := filepath.Join(CacheRoot(cwd), filepath.FromSlash(slug))
	repo := &Repo{URL: cloneURL, Ref: ref, LocalPath: dest}

	if isGitRepo(dest) {
		repo.Cached = true
		return repo, nil
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return nil, fmt.Errorf("create cache dir: %w", err)
	}

	args := []string{"clone", "--depth", "1", "--quiet"}
	if ref != "" {
		args = append(args, "--branch", ref)
	}
	args = append(args, cloneURL, dest)

	cmd := exec.CommandContext(ctx, "git", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		_ = os.RemoveAll(dest) // drop any partial clone
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("clone %s: %s", cloneURL, msg)
	}
	return repo, nil
}

// List returns the repos currently cached under cwd. Order follows the
// filesystem walk.
func List(cwd string) []Repo {
	root := CacheRoot(cwd)
	var repos []Repo
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		if d.Name() == ".git" {
			return filepath.SkipDir
		}
		if isGitRepo(path) {
			rel, _ := filepath.Rel(root, path)
			repos = append(repos, Repo{
				URL:       "https://" + filepath.ToSlash(rel),
				LocalPath: path,
				Cached:    true,
			})
			return filepath.SkipDir // don't descend into a repo
		}
		return nil
	})
	return repos
}

// FileList returns up to max file paths (relative to the repo) via ripgrep,
// plus the total count rg reported.
func FileList(localPath string, max int) (string, int) {
	cmd := exec.Command("rg", "--files")
	cmd.Dir = localPath
	out, err := cmd.Output()
	if err != nil {
		return "", 0
	}
	all := strings.Split(strings.TrimSpace(string(out)), "\n")
	total := len(all)
	if max > 0 && len(all) > max {
		all = all[:max]
	}
	return strings.Join(all, "\n"), total
}

// Search runs ripgrep for pattern inside a cached repo and returns up to
// maxLines matching lines (path:line:text). It's how you follow a call site
// through the real source.
func Search(ctx context.Context, localPath, pattern string, maxLines int) (string, error) {
	if maxLines <= 0 {
		maxLines = 40
	}
	cmd := exec.CommandContext(ctx, "rg", "-n", "--no-heading", "-S", pattern)
	cmd.Dir = localPath
	out, _ := cmd.Output() // rg exits 1 on no match; treat as empty
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return "", nil
	}
	if len(lines) > maxLines {
		lines = append(lines[:maxLines], fmt.Sprintf("... (%d more matches)", len(lines)-maxLines))
	}
	return strings.Join(lines, "\n"), nil
}

func isGitRepo(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil && (info.IsDir() || info.Mode().IsRegular())
}

// normalize turns a repo reference into an https clone URL and a sanitized
// host/owner/repo slug safe to use as a filesystem path.
func normalize(raw string) (cloneURL, slug string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", errors.New("empty repository reference")
	}

	var host, path string
	switch {
	case schemeRE.MatchString(raw):
		u, e := url.Parse(raw)
		if e != nil {
			return "", "", fmt.Errorf("parse %q: %w", raw, e)
		}
		host, path = u.Host, strings.Trim(u.Path, "/")
		cloneURL = raw
	case scpRE.MatchString(raw):
		m := scpRE.FindStringSubmatch(raw)
		host, path = m[1], strings.TrimSuffix(strings.Trim(m[2], "/"), ".git")
		cloneURL = "https://" + host + "/" + path
	default:
		parts := strings.SplitN(raw, "/", 2)
		if len(parts) == 2 && strings.Contains(parts[0], ".") {
			host, path = parts[0], strings.Trim(parts[1], "/")
		} else {
			host, path = "github.com", strings.Trim(raw, "/")
		}
		path = strings.TrimSuffix(path, ".git")
		cloneURL = "https://" + host + "/" + path
	}

	path = strings.TrimSuffix(path, ".git")
	if host == "" || path == "" {
		return "", "", fmt.Errorf("cannot parse repository reference: %q", raw)
	}
	return cloneURL, slugify(host + "/" + path), nil
}

func slugify(s string) string {
	var segs []string
	for _, seg := range strings.Split(strings.ToLower(s), "/") {
		seg = strings.Trim(unsafeRE.ReplaceAllString(seg, "-"), "-")
		if seg == "" || seg == "." || seg == ".." {
			continue
		}
		segs = append(segs, seg)
	}
	return strings.Join(segs, "/")
}
