#!/usr/bin/env python3
"""
Color Palette Generator - Creates beautiful color schemes
"""

import colorsys
import random

class ColorPalette:
    def __init__(self):
        self.colors = []
    
    def generate_complementary(self, base_color):
        """Generate complementary colors"""
        r, g, b = base_color
        h, s, v = colorsys.rgb_to_hsv(r/255, g/255, b/255)
        
        # Complementary color (180 degrees opposite)
        comp_h = (h + 0.5) % 1.0
        comp_rgb = colorsys.hsv_to_rgb(comp_h, s, v)
        
        return [
            base_color,
            tuple(int(c * 255) for c in comp_rgb)
        ]
    
    def generate_triadic(self, base_color):
        """Generate triadic color scheme"""
        r, g, b = base_color
        h, s, v = colorsys.rgb_to_hsv(r/255, g/255, b/255)
        
        colors = []
        for i in range(3):
            new_h = (h + i * 0.333) % 1.0
            rgb = colorsys.hsv_to_rgb(new_h, s, v)
            colors.append(tuple(int(c * 255) for c in rgb))
        
        return colors
    
    def generate_analogous(self, base_color, count=5):
        """Generate analogous color scheme"""
        r, g, b = base_color
        h, s, v = colorsys.rgb_to_hsv(r/255, g/255, b/255)
        
        colors = []
        step = 0.1 / count
        for i in range(count):
            new_h = (h + (i - count//2) * step) % 1.0
            rgb = colorsys.hsv_to_rgb(new_h, s, v)
            colors.append(tuple(int(c * 255) for c in rgb))
        
        return colors
    
    def to_hex(self, color):
        """Convert RGB to hex"""
        return f"#{color[0]:02x}{color[1]:02x}{color[2]:02x}"
    
    def display_palette(self, colors, name="Color Palette"):
        """Display colors as ASCII art"""
        print(f"\n{name}:")
        print("=" * 50)
        
        for i, color in enumerate(colors):
            hex_color = self.to_hex(color)
            rgb_str = f"RGB({color[0]}, {color[1]}, {color[2]})"
            
            # Create a visual representation
            brightness = sum(color) / (255 * 3)
            block = "█" if brightness > 0.5 else "▓"
            
            print(f"{i+1}. {block * 20} {hex_color} - {rgb_str}")

if __name__ == "__main__":
    palette = ColorPalette()
    
    # Test with a nice blue
    base = (70, 130, 180)  # Steel blue
    
    print("🎨 Color Palette Generator")
    print("Base color: Steel Blue", palette.to_hex(base))
    
    comp = palette.generate_complementary(base)
    palette.display_palette(comp, "Complementary Colors")
    
    triadic = palette.generate_triadic(base)
    palette.display_palette(triadic, "Triadic Harmony")
    
    analogous = palette.generate_analogous(base)
    palette.display_palette(analogous, "Analogous Scheme")