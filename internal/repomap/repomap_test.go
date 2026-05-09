package repomap

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildRepoMapIncludesGoAndPythonSymbols(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("rg is not installed")
	}

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "main.go"), `package sample

type Widget struct{}
type Runner interface {
	Run()
}

func BuildWidget() Widget {
	return Widget{}
}
`)
	writeFile(t, filepath.Join(dir, "worker.py"), `class Worker:
    pass

def run_worker():
    pass
`)

	got := BuildRepoMap(dir)
	for _, want := range []string{
		"main.go:",
		"package sample",
		"Widget",
		"Runner",
		"BuildWidget",
		"worker.py:",
		"Worker",
		"run_worker",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("repo map missing %q:\n%s", want, got)
		}
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
