package main

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
)

type WordStats struct {
	Word      string
	Count     int
	Length    int
	Vowels    int
	Consonants int
	IsPalindrome bool
}

func analyzeText(text string) map[string]*WordStats {
	stats := make(map[string]*WordStats)
	words := strings.Fields(text)
	
	for _, word := range words {
		cleaned := cleanWord(word)
		if cleaned == "" {
			continue
		}
		
		if _, exists := stats[cleaned]; !exists {
			stats[cleaned] = &WordStats{
				Word:         cleaned,
				Count:        0,
				Length:       len(cleaned),
				Vowels:       countVowels(cleaned),
				IsPalindrome: isPalindrome(cleaned),
			}
			stats[cleaned].Consonants = stats[cleaned].Length - stats[cleaned].Vowels
		}
		stats[cleaned].Count++
	}
	
	return stats
}

func cleanWord(word string) string {
	var result []rune
	for _, r := range word {
		if unicode.IsLetter(r) {
			result = append(result, unicode.ToLower(r))
		}
	}
	return string(result)
}

func countVowels(word string) int {
	vowels := "aeiou"
	count := 0
	for _, r := range word {
		if strings.ContainsRune(vowels, r) {
			count++
		}
	}
	return count
}

func isPalindrome(word string) bool {
	if len(word) < 2 {
		return false
	}
	runes := []rune(word)
	for i := 0; i < len(runes)/2; i++ {
		if runes[i] != runes[len(runes)-1-i] {
			return false
		}
	}
	return true
}

func displayStats(stats map[string]*WordStats) {
	// Sort by frequency
	var words []*WordStats
	for _, stat := range stats {
		words = append(words, stat)
	}
	sort.Slice(words, func(i, j int) bool {
		return words[i].Count > words[j].Count
	})
	
	fmt.Println("\n📊 Text Analysis Report")
	fmt.Println("=" + strings.Repeat("=", 70))
	
	fmt.Printf("Total unique words: %d\n\n", len(words))
	
	// Most frequent words
	fmt.Println("🔝 Top 10 Most Frequent Words:")
	for i := 0; i < 10 && i < len(words); i++ {
		w := words[i]
		fmt.Printf("%2d. %-15s (count: %d, length: %d, vowels: %d)\n",
			i+1, w.Word, w.Count, w.Length, w.Vowels)
	}
	
	// Find palindromes
	fmt.Println("\n🔄 Palindromes found:")
	palindromeCount := 0
	for _, w := range words {
		if w.IsPalindrome {
			fmt.Printf("  - %s\n", w.Word)
			palindromeCount++
		}
	}
	if palindromeCount == 0 {
		fmt.Println("  (none found)")
	}
	
	// Longest words
	sort.Slice(words, func(i, j int) bool {
		return words[i].Length > words[j].Length
	})
	
	fmt.Println("\n📏 Longest words:")
	for i := 0; i < 5 && i < len(words); i++ {
		w := words[i]
		fmt.Printf("  %d. %s (%d letters)\n", i+1, w.Word, w.Length)
	}
}

func main() {
	sampleText := `The quick brown fox jumps over the lazy dog. 
	A man, a plan, a canal: Panama! Was it a car or a cat I saw? 
	Madam, in Eden, I'm Adam. Never odd or even. 
	The programming language Go is simple, reliable, and efficient.`
	
	fmt.Println("📝 Analyzing text...")
	stats := analyzeText(sampleText)
	displayStats(stats)
}