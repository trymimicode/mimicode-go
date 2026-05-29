#!/usr/bin/env python3
"""A silly joke generator that tells programming jokes"""

import random
import time

class JokeGenerator:
    def __init__(self):
        self.jokes = [
            "Why do programmers prefer dark mode? Because light attracts bugs!",
            "404: Joke not found!",
            "Segmentation fault (core dumped)",
            "Why do programmers always mix up Christmas and Halloween? Because Oct 31 == Dec 25!",
            "A SQL query walks into a bar, walks up to two tables and asks... 'Can I join you?'",
            "['hip', 'hip'] (hip hip array!)",
            "Why did the developer go broke? Because he used up all his cache!",
            "There are only 10 types of people in the world: those who understand binary and those who don't."
        ]
        self.used_jokes = []
    
    def tell_joke(self):
        """Tell a random joke with dramatic timing"""
        available_jokes = [j for j in self.jokes if j not in self.used_jokes]
        
        if not available_jokes:
            print("\n🔄 All jokes told! Resetting the joke vault...")
            self.used_jokes = []
            available_jokes = self.jokes
        
        joke = random.choice(available_jokes)
        self.used_jokes.append(joke)
        
        print(f"\n💡 {joke}")
        
        return joke
    
    def add_custom_joke(self, joke):
        """Add a custom joke to the collection"""
        self.jokes.append(joke)
        print(f"✅ Added your joke! Now I know {len(self.jokes)} jokes!")
    
    def joke_battle(self):
        """Interactive joke battle mode"""
        print("\n⚔️  JOKE BATTLE MODE ⚔️")
        print("I'll tell a joke, then you tell one!")
        
        rounds = 3
        for round in range(1, rounds + 1):
            print(f"\n--- Round {round} ---")
            self.tell_joke()
            
            user_joke = input("\nYour turn! Tell me a joke (or 'skip' to pass): ")
            if user_joke.lower() != 'skip':
                print("😂 Haha, that's a good one!")
                if input("Should I remember this joke? (y/n): ").lower() == 'y':
                    self.add_custom_joke(user_joke)
        
        print("\n🏆 Great joke battle! Thanks for playing!")

if __name__ == "__main__":
    generator = JokeGenerator()
    
    print("🤖 Welcome to the Joke Generator!")
    print("Commands: 'joke', 'add', 'battle', 'quit'")
    
    while True:
        command = input("\nWhat would you like to do? ").lower()
        
        if command == 'joke':
            generator.tell_joke()
        elif command == 'add':
            custom = input("Enter your joke: ")
            generator.add_custom_joke(custom)
        elif command == 'battle':
            generator.joke_battle()
        elif command == 'quit':
            print("\n👋 Thanks for laughing with me! Bye!")
            break
        else:
            print("❓ Unknown command. Try 'joke', 'add', 'battle', or 'quit'")