package main

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

// Function that reads input (testable version)
func processUserInput(reader io.Reader) (string, int, []string, error) {
	var name string
	var age int
	var colors []string
	
	// Read name
	fmt.Fscanf(reader, "%s\n", &name)
	
	// Read age
	fmt.Fscanf(reader, "%d\n", &age)
	
	// Read colors
	var colorsLine string
	fmt.Fscanf(reader, "%s %s %s\n", &colorsLine)
	colors = strings.Fields(colorsLine)
	
	return name, age, colors, nil
}

func TestProcessUserInput(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		expectedName  string
		expectedAge   int
		expectedColors []string
	}{
		{
			name:          "Valid input",
			input:         "Alice\n25\nred blue green\n",
			expectedName:  "Alice",
			expectedAge:   25,
			expectedColors: []string{"red", "blue", "green"},
		},
		{
			name:          "Single word name",
			input:         "Bob\n30\nyellow orange purple\n",
			expectedName:  "Bob",
			expectedAge:   30,
			expectedColors: []string{"yellow", "orange", "purple"},
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := strings.NewReader(tt.input)
			name, age, colors, err := processUserInput(reader)
			
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			
			if name != tt.expectedName {
				t.Errorf("name = %q, want %q", name, tt.expectedName)
			}
			
			if age != tt.expectedAge {
				t.Errorf("age = %d, want %d", age, tt.expectedAge)
			}
			
			if len(colors) != len(tt.expectedColors) {
				t.Errorf("colors length = %d, want %d", len(colors), len(tt.expectedColors))
			}
		})
	}
}

// Example of testing with simulated stdin
func TestWithSimulatedStdin(t *testing.T) {
	// Simulate user input
	input := "TestUser\n42\nred green blue\n"
	reader := strings.NewReader(input)
	
	// In real code, you would pass this reader to your function
	// instead of using os.Stdin
	buf := new(bytes.Buffer)
	_, err := io.Copy(buf, reader)
	if err != nil {
		t.Fatal(err)
	}
	
	// Verify the buffer contains our input
	if buf.String() != input {
		t.Errorf("Buffer content = %q, want %q", buf.String(), input)
	}
}