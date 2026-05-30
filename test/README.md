# Test Example Code

This folder contains example Go code with tests and benchmarks.

## Files

- `hello.go` - Main program with utility functions
  - `Greeting()` - Returns personalized greetings
  - `Calculate()` - Performs basic arithmetic operations
  
- `hello_test.go` - Unit tests and benchmarks
  - Tests for all functions
  - Benchmark tests for performance measurement

## Running the Code

```bash
# Run the main program
go run hello.go

# Run tests
go test

# Run tests with verbose output
go test -v

# Run benchmarks
go test -bench=.

# Run with test coverage
go test -cover
```

## Example Output

```
Hello, World!
Hello, Alice!
10 add 5 = 15
10 subtract 5 = 5
10 multiply 5 = 50
10 divide 5 = 2

String operations:
Original: Hello, Go Programming!
Uppercase: HELLO, GO PROGRAMMING!
Lowercase: hello, go programming!
Contains 'Go': true
```