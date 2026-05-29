#!/usr/bin/env python3
"""
Maze Generator and Solver using recursive backtracking
"""

import random
import time
from collections import deque

class Maze:
    def __init__(self, width=25, height=15):
        self.width = width
        self.height = height
        self.grid = [[1 for _ in range(width)] for _ in range(height)]
        self.solution = []
        
    def generate(self):
        """Generate maze using recursive backtracking"""
        # Start from (1, 1)
        stack = [(1, 1)]
        self.grid[1][1] = 0
        
        directions = [(0, 2), (2, 0), (0, -2), (-2, 0)]
        
        while stack:
            x, y = stack[-1]
            
            # Find unvisited neighbors
            neighbors = []
            for dx, dy in directions:
                nx, ny = x + dx, y + dy
                if (0 < nx < self.width - 1 and 0 < ny < self.height - 1 
                    and self.grid[ny][nx] == 1):
                    neighbors.append((nx, ny, dx, dy))
            
            if neighbors:
                # Choose random neighbor
                nx, ny, dx, dy = random.choice(neighbors)
                # Carve path
                self.grid[ny][nx] = 0
                self.grid[y + dy//2][x + dx//2] = 0
                stack.append((nx, ny))
            else:
                stack.pop()
        
        # Set entrance and exit
        self.grid[1][0] = 0  # Entrance
        self.grid[self.height - 2][self.width - 1] = 0  # Exit
    
    def solve(self, start=(0, 1), end=None):
        """Solve maze using BFS"""
        if end is None:
            end = (self.width - 1, self.height - 2)
        
        queue = deque([start])
        visited = {start: None}
        
        directions = [(0, 1), (1, 0), (0, -1), (-1, 0)]
        
        while queue:
            x, y = queue.popleft()
            
            if (x, y) == end:
                # Reconstruct path
                path = []
                current = (x, y)
                while current is not None:
                    path.append(current)
                    current = visited[current]
                self.solution = path[::-1]
                return True
            
            for dx, dy in directions:
                nx, ny = x + dx, y + dy
                if (0 <= nx < self.width and 0 <= ny < self.height 
                    and (nx, ny) not in visited and self.grid[ny][nx] == 0):
                    visited[(nx, ny)] = (x, y)
                    queue.append((nx, ny))
        
        return False
    
    def display(self, show_solution=False):
        """Display the maze"""
        chars = {
            'wall': '█',
            'path': ' ',
            'solution': '·',
            'start': 'S',
            'end': 'E'
        }
        
        # Create display grid
        display = []
        for y in range(self.height):
            row = []
            for x in range(self.width):
                if self.grid[y][x] == 1:
                    row.append(chars['wall'])
                else:
                    row.append(chars['path'])
            display.append(row)
        
        # Mark solution path
        if show_solution and self.solution:
            for x, y in self.solution[1:-1]:
                display[y][x] = chars['solution']
        
        # Mark start and end
        if self.solution:
            sx, sy = self.solution[0]
            ex, ey = self.solution[-1]
            display[sy][sx] = chars['start']
            display[ey][ex] = chars['end']
        
        # Print the maze
        print('\n'.join(''.join(row) for row in display))
    
    def animate_solve(self, delay=0.05):
        """Animate the solving process"""
        start = (0, 1)
        end = (self.width - 1, self.height - 2)
        
        queue = deque([start])
        visited = {start: None}
        directions = [(0, 1), (1, 0), (0, -1), (-1, 0)]
        
        while queue:
            x, y = queue.popleft()
            
            # Show current exploration
            self.display()
            print(f"\nExploring: ({x}, {y})")
            time.sleep(delay)
            print('\033[F' * (self.height + 2))  # Move cursor up
            
            if (x, y) == end:
                visited_path = []
                current = (x, y)
                while current is not None:
                    visited_path.append(current)
                    current = visited[current]
                self.solution = visited_path[::-1]
                return True
            
            for dx, dy in directions:
                nx, ny = x + dx, y + dy
                if (0 <= nx < self.width and 0 <= ny < self.height 
                    and (nx, ny) not in visited and self.grid[ny][nx] == 0):
                    visited[(nx, ny)] = (x, y)
                    queue.append((nx, ny))
        
        return False

def main():
    print("🧩 Maze Generator & Solver")
    print("=" * 40)
    
    # Generate maze
    maze = Maze(41, 21)
    print("\nGenerating maze...")
    maze.generate()
    maze.display()
    
    print("\n\nPress Enter to solve the maze...")
    input()
    
    # Solve maze
    print("Solving maze...")
    if maze.solve():
        maze.display(show_solution=True)
        print(f"\n✅ Solution found! Path length: {len(maze.solution)}")
    else:
        print("\n❌ No solution exists!")
    
    # Optional animation
    print("\nWould you like to see an animated solution? (y/n): ", end='')
    if input().lower() == 'y':
        maze2 = Maze(25, 15)
        maze2.generate()
        print("\nAnimating solution process...\n")
        maze2.animate_solve()
        maze2.display(show_solution=True)
        print("\nAnimation complete!")

if __name__ == "__main__":
    main()