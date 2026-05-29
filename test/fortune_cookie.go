package main

import (
    "fmt"
    "math/rand"
    "time"
)

// FortuneCookie generates random fortune cookie messages
type FortuneCookie struct {
    fortunes []string
    lucky    []int
}

func NewFortuneCookie() *FortuneCookie {
    return &FortuneCookie{
        fortunes: []string{
            "🔮 Your code will compile on the first try today!",
            "🐛 A bug you've been hunting will reveal itself soon",
            "💡 An elegant solution awaits in the shower",
            "☕ Coffee will taste extra good during your next debug session",
            "🌟 Your pull request will be approved without changes",
            "🎯 You will find the missing semicolon within 5 minutes",
            "🚀 Performance optimization ideas will flow freely",
            "📚 Documentation will actually be helpful today",
            "🔧 That legacy code won't be as scary as it looks",
            "✨ Your variable names will be self-documenting",
            "🎪 The circus of callbacks will make sense",
            "🏗️ Your architecture decisions will age well",
            "🧩 That regex will work on the second attempt",
            "🎨 Your UI will be pixel-perfect without CSS struggles",
            "🔄 Git merge conflicts will resolve themselves peacefully",
        },
        lucky: []int{},
    }
}

func (fc *FortuneCookie) GetFortune() string {
    return fc.fortunes[rand.Intn(len(fc.fortunes))]
}

func (fc *FortuneCookie) GenerateLuckyNumbers(count int) []int {
    fc.lucky = []int{}
    used := make(map[int]bool)
    
    for len(fc.lucky) < count {
        num := rand.Intn(100) + 1
        if !used[num] {
            fc.lucky = append(fc.lucky, num)
            used[num] = true
        }
    }
    
    return fc.lucky
}

func (fc *FortuneCookie) CrackCookie() {
    fmt.Println("\n🥠 *crack* Opening your fortune cookie...")
    time.Sleep(1 * time.Second)
    
    fmt.Printf("\n📜 %s\n", fc.GetFortune())
    
    luckyNums := fc.GenerateLuckyNumbers(6)
    fmt.Print("\n🎰 Lucky numbers: ")
    for i, num := range luckyNums {
        if i > 0 {
            fmt.Print(" - ")
        }
        fmt.Printf("%d", num)
    }
    fmt.Println("\n")
}

// ASCII art cookie
func printCookie() {
    cookie := `
      🥠
    `
    fmt.Println(cookie)
}

func main() {
    rand.Seed(time.Now().UnixNano())
    fc := NewFortuneCookie()
    
    fmt.Println("🥠 Welcome to the Fortune Cookie Generator!")
    printCookie()
    
    for {
        fmt.Print("\nPress Enter for a fortune (or 'q' to quit): ")
        
        var input string
        fmt.Scanln(&input)
        
        if input == "q" {
            fmt.Println("\n👋 May your code be bug-free!")
            break
        }
        
        fc.CrackCookie()
    }
}