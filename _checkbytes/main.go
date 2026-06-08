package main

import (
	"bytes"
	"fmt"
	"os"
)

func main() {
	data, _ := os.ReadFile("internal/tui/tui.go")
	idx := bytes.Index(data, []byte("diffLineBg re-applies"))
	fmt.Printf("%q\n", string(data[idx-3:idx+210]))
}
