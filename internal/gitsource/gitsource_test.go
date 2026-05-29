package gitsource

import (
	"path/filepath"
	"testing"
)

func TestNormalize(t *testing.T) {
	cases := []struct {
		raw       string
		wantURL   string
		wantSlug  string
		wantError bool
	}{
		{"owner/repo", "https://github.com/owner/repo", "github.com/owner/repo", false},
		{"jackc/pgx", "https://github.com/jackc/pgx", "github.com/jackc/pgx", false},
		{"github.com/owner/repo", "https://github.com/owner/repo", "github.com/owner/repo", false},
		{"https://github.com/owner/repo", "https://github.com/owner/repo", "github.com/owner/repo", false},
		{"https://github.com/owner/repo.git", "https://github.com/owner/repo.git", "github.com/owner/repo", false},
		{"git@github.com:owner/repo.git", "https://github.com/owner/repo", "github.com/owner/repo", false},
		{"https://gitlab.com/group/sub/proj", "https://gitlab.com/group/sub/proj", "gitlab.com/group/sub/proj", false},
		{"", "", "", true},
	}
	for _, c := range cases {
		gotURL, gotSlug, err := normalize(c.raw)
		if c.wantError {
			if err == nil {
				t.Errorf("normalize(%q): expected error, got none", c.raw)
			}
			continue
		}
		if err != nil {
			t.Errorf("normalize(%q): unexpected error: %v", c.raw, err)
			continue
		}
		if gotURL != c.wantURL {
			t.Errorf("normalize(%q) url = %q, want %q", c.raw, gotURL, c.wantURL)
		}
		if gotSlug != c.wantSlug {
			t.Errorf("normalize(%q) slug = %q, want %q", c.raw, gotSlug, c.wantSlug)
		}
	}
}

func TestSlugifyRejectsTraversal(t *testing.T) {
	got := slugify("github.com/../../etc/passwd")
	if filepath.IsAbs(filepath.FromSlash(got)) {
		t.Fatalf("slug %q resolved to absolute path", got)
	}
	for _, seg := range []string{"..", "."} {
		for _, s := range filepathSegments(got) {
			if s == seg {
				t.Fatalf("slug %q contains traversal segment %q", got, seg)
			}
		}
	}
}

func filepathSegments(slug string) []string {
	var out []string
	cur := ""
	for _, r := range slug {
		if r == '/' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	return append(out, cur)
}

func TestCacheRoot(t *testing.T) {
	got := CacheRoot("/tmp/proj")
	want := filepath.Join("/tmp/proj", ".mimi", "cache")
	if got != want {
		t.Errorf("CacheRoot = %q, want %q", got, want)
	}
}
