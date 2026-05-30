package main

import (
    "testing"
)

func TestGreeting(t *testing.T) {
    tests := []struct {
        name     string
        input    string
        expected string
    }{
        {
            name:     "empty name",
            input:    "",
            expected: "Hello, World!",
        },
        {
            name:     "with name",
            input:    "alice",
            expected: "Hello, Alice!",
        },
        {
            name:     "with uppercase name",
            input:    "BOB",
            expected: "Hello, Bob!",
        },
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got := Greeting(tt.input)
            if got != tt.expected {
                t.Errorf("Greeting(%q) = %q, want %q", tt.input, got, tt.expected)
            }
        })
    }
}

func TestCalculate(t *testing.T) {
    tests := []struct {
        name      string
        a, b      int
        operation string
        expected  int
        wantErr   bool
    }{
        {"add", 10, 5, "add", 15, false},
        {"subtract", 10, 5, "subtract", 5, false},
        {"multiply", 10, 5, "multiply", 50, false},
        {"divide", 10, 5, "divide", 2, false},
        {"divide by zero", 10, 0, "divide", 0, true},
        {"unknown operation", 10, 5, "modulo", 0, true},
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got, err := Calculate(tt.a, tt.b, tt.operation)
            if (err != nil) != tt.wantErr {
                t.Errorf("Calculate(%d, %d, %q) error = %v, wantErr %v", tt.a, tt.b, tt.operation, err, tt.wantErr)
                return
            }
            if !tt.wantErr && got != tt.expected {
                t.Errorf("Calculate(%d, %d, %q) = %d, want %d", tt.a, tt.b, tt.operation, got, tt.expected)
            }
        })
    }
}

func BenchmarkGreeting(b *testing.B) {
    for i := 0; i < b.N; i++ {
        Greeting("benchmark")
    }
}

func BenchmarkCalculate(b *testing.B) {
    for i := 0; i < b.N; i++ {
        Calculate(100, 50, "add")
    }
}