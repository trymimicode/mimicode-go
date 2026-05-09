package repomap

import (
	"bytes"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

const maxRepoMapChars = 4000

var (
	pySymbolRE = regexp.MustCompile(`^(def|class)\s+([A-Za-z_][A-Za-z0-9_]*)`)
	jsSymbolRE = regexp.MustCompile(`^(?:export\s+)?(?:(function|class)\s+([A-Za-z_$][A-Za-z0-9_$]*)|const\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*=)`)
)

// BuildRepoMap returns a compact symbol summary for nearby source files.
func BuildRepoMap(cwd string) string {
	cmd := exec.Command("rg", "--files", "-t", "go", "-t", "py", "-t", "js", "-t", "ts", "--max-depth", "4")
	cmd.Dir = cwd
	out, err := cmd.Output()
	if errors.Is(err, exec.ErrNotFound) || len(bytes.TrimSpace(out)) == 0 {
		return ""
	}

	var lines []string
	for _, path := range strings.Split(string(out), "\n") {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		symbols, ok := symbolsForFile(cwd, path)
		if !ok || len(symbols) == 0 {
			continue
		}
		lines = append(lines, filepath.ToSlash(path)+": "+strings.Join(symbols, ", "))
	}

	result := strings.Join(lines, "\n")
	if len(result) > maxRepoMapChars {
		return result[:maxRepoMapChars] + "... (truncated)"
	}
	return result
}

func symbolsForFile(cwd, path string) ([]string, bool) {
	switch filepath.Ext(path) {
	case ".go":
		return goSymbols(filepath.Join(cwd, path))
	case ".py":
		return scanSymbols(filepath.Join(cwd, path), pySymbolRE)
	case ".js", ".ts":
		return scanSymbols(filepath.Join(cwd, path), jsSymbolRE)
	default:
		return nil, false
	}
}

func goSymbols(path string) ([]string, bool) {
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil, false
	}

	symbols := []string{"package " + file.Name.Name}
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Recv == nil {
				symbols = append(symbols, d.Name.Name)
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				typeSpec, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				switch typeSpec.Type.(type) {
				case *ast.StructType, *ast.InterfaceType:
					symbols = append(symbols, typeSpec.Name.Name)
				}
			}
		}
	}
	return symbols, true
}

func scanSymbols(path string, re *regexp.Regexp) ([]string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}

	var symbols []string
	for _, line := range strings.Split(string(data), "\n") {
		matches := re.FindStringSubmatch(strings.TrimRight(line, "\r"))
		if matches == nil {
			continue
		}
		for i := 2; i < len(matches); i++ {
			if matches[i] != "" {
				symbols = append(symbols, matches[i])
				break
			}
		}
	}
	return symbols, true
}
