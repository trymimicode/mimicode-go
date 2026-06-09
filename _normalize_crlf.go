//go:build ignore

package main

import (
	"bytes"
	"fmt"
	"os"
)

func main() {
	targets := []string{
		"internal/tui/tui.go",
		"internal/store/store.go",
	}
	for _, path := range targets {
		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		n := bytes.Count(data, []byte("\r\n"))
		normalized := bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
		if err := os.WriteFile(path, normalized, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("normalized %d CRLF->LF in %s\n", n, path)
	}
}
