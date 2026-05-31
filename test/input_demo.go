package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

func main() {
	reader := bufio.NewReader(os.Stdin)
	
	// Simple string input
	fmt.Print("Enter your name: ")
	name, _ := reader.ReadString('\n')
	name = strings.TrimSpace(name)
	fmt.Printf("Hello, %s!\n\n", name)
	
	// Number input with validation
	fmt.Print("Enter your age: ")
	ageStr, _ := reader.ReadString('\n')
	ageStr = strings.TrimSpace(ageStr)
	
	var age int
	_, err := fmt.Sscanf(ageStr, "%d", &age)
	if err != nil {
		fmt.Println("Invalid age input!")
	} else {
		fmt.Printf("You are %d years old.\n\n", age)
	}
	
	// Multiple inputs
	fmt.Print("Enter three favorite colors (space-separated): ")
	colorsLine, _ := reader.ReadString('\n')
	colors := strings.Fields(colorsLine)
	
	fmt.Println("Your favorite colors are:")
	for i, color := range colors {
		fmt.Printf("%d. %s\n", i+1, color)
	}
}