package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/trymimicode/mimicode-go/internal/langpack"
)

func runInstallCmd(args []string, cwd string, out, errOut io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(errOut, "usage: mimicode install <language>")
		fmt.Fprintln(errOut, "  installs a language pack from trymimicode/language-packs into .mimi/")
		return 2
	}
	language := strings.ToLower(strings.TrimSpace(args[0]))
	fmt.Fprintf(errOut, "installing %s language pack…\n", language)
	if err := langpack.Install(cwd, language); err != nil {
		fmt.Fprintf(errOut, "mimicode: install: %v\n", err)
		return 1
	}
	fmt.Fprintf(out, "✓ installed %s language pack\n", language)
	fmt.Fprintf(out, "  • .mimi/AGENTS.md  — language conventions (your AGENTS.md merged in)\n")
	fmt.Fprintf(out, "  • .mimi/RULES.md   — behavioral rules appended\n")
	fmt.Fprintf(out, "  • .mimi/packs.lock — install record updated\n")
	return 0
}
