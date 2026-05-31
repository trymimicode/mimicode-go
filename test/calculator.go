package main

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// Calculator with dependency injection for testing
type Calculator struct {
	reader *bufio.Reader
	writer io.Writer
}

func NewCalculator(reader io.Reader, writer io.Writer) *Calculator {
	return &Calculator{
		reader: bufio.NewReader(reader),
		writer: writer,
	}
}

func (c *Calculator) Run() {
	fmt.Fprintln(c.writer, "Simple Calculator")
	fmt.Fprintln(c.writer, "Commands: add, subtract, multiply, divide, quit")
	
	for {
		fmt.Fprint(c.writer, "> ")
		
		cmd, err := c.reader.ReadString('\n')
		if err != nil {
			break
		}
		
		cmd = strings.TrimSpace(strings.ToLower(cmd))
		
		if cmd == "quit" {
			fmt.Fprintln(c.writer, "Goodbye!")
			break
		}
		
		switch cmd {
		case "add", "subtract", "multiply", "divide":
			c.performOperation(cmd)
		default:
			fmt.Fprintln(c.writer, "Unknown command")
		}
	}
}

func (c *Calculator) performOperation(op string) {
	fmt.Fprint(c.writer, "Enter first number: ")
	var a, b float64
	
	line1, _ := c.reader.ReadString('\n')
	fmt.Sscanf(line1, "%f", &a)
	
	fmt.Fprint(c.writer, "Enter second number: ")
	line2, _ := c.reader.ReadString('\n')
	fmt.Sscanf(line2, "%f", &b)
	
	var result float64
	switch op {
	case "add":
		result = a + b
	case "subtract":
		result = a - b
	case "multiply":
		result = a * b
	case "divide":
		if b != 0 {
			result = a / b
		} else {
			fmt.Fprintln(c.writer, "Error: Division by zero")
			return
		}
	}
	
	fmt.Fprintf(c.writer, "Result: %.2f\n", result)
}

// For running the actual program
func main() {
	calc := NewCalculator(os.Stdin, os.Stdout)
	calc.Run()
}