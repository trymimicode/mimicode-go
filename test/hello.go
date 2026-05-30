package main

import (
    "fmt"
    "strings"
)

// Greeting returns a personalized greeting message
func Greeting(name string) string {
    if name == "" {
        return "Hello, World!"
    }
    return fmt.Sprintf("Hello, %s!", strings.Title(name))
}

// Calculate performs basic arithmetic operations
func Calculate(a, b int, operation string) (int, error) {
    switch operation {
    case "add":
        return a + b, nil
    case "subtract":
        return a - b, nil
    case "multiply":
        return a * b, nil
    case "divide":
        if b == 0 {
            return 0, fmt.Errorf("cannot divide by zero")
        }
        return a / b, nil
    default:
        return 0, fmt.Errorf("unknown operation: %s", operation)
    }
}

func main() {
    // Example 1: Greetings
    fmt.Println(Greeting(""))
    fmt.Println(Greeting("Alice"))
    
    // Example 2: Calculations
    operations := []struct {
        a, b int
        op   string
    }{
        {10, 5, "add"},
        {10, 5, "subtract"},
        {10, 5, "multiply"},
        {10, 5, "divide"},
    }
    
    for _, calc := range operations {
        result, err := Calculate(calc.a, calc.b, calc.op)
        if err != nil {
            fmt.Printf("Error: %v\n", err)
        } else {
            fmt.Printf("%d %s %d = %d\n", calc.a, calc.op, calc.b, result)
        }
    }
    
    // Example 3: String manipulation
    text := "Hello, Go Programming!"
    fmt.Println("\nString operations:")
    fmt.Printf("Original: %s\n", text)
    fmt.Printf("Uppercase: %s\n", strings.ToUpper(text))
    fmt.Printf("Lowercase: %s\n", strings.ToLower(text))
    fmt.Printf("Contains 'Go': %v\n", strings.Contains(text, "Go"))
}