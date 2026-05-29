// Emoji Rain - A fun terminal animation
// Watch emojis rain down your console!

const emojis = ['🌟', '⭐', '✨', '💫', '🌙', '☄️', '🌈', '🦄', '🎈', '🎉', '🎊', '🍕', '🍔', '🌮', '🍰', '🍪', '🍩', '🎮', '🎯', '🎪', '🎭', '🎨', '🎬', '🎸', '🎵', '🎶', '🚀', '🛸', '🪐', '🌎', '❤️', '💖', '💝', '💗', '💕'];

class EmojiRain {
    constructor(width = 80, height = 20) {
        this.width = width;
        this.height = height;
        this.drops = [];
        this.maxDrops = Math.floor(width / 4);
    }

    createDrop() {
        return {
            x: Math.floor(Math.random() * this.width),
            y: 0,
            emoji: emojis[Math.floor(Math.random() * emojis.length)],
            speed: Math.random() * 0.5 + 0.5,
            trail: []
        };
    }

    update() {
        // Move existing drops
        this.drops.forEach(drop => {
            drop.trail.unshift({ x: drop.x, y: drop.y, emoji: drop.emoji });
            if (drop.trail.length > 3) {
                drop.trail.pop();
            }
            drop.y += drop.speed;
        });

        // Remove drops that have fallen off screen
        this.drops = this.drops.filter(drop => drop.y < this.height);

        // Add new drops
        if (this.drops.length < this.maxDrops && Math.random() < 0.3) {
            this.drops.push(this.createDrop());
        }
    }

    render() {
        // Clear screen (ANSI escape code)
        console.clear();
        
        // Create display grid
        const grid = Array(this.height).fill(null).map(() => 
            Array(this.width).fill(' ')
        );

        // Place emojis on grid
        this.drops.forEach(drop => {
            // Draw trail with fading effect
            drop.trail.forEach((pos, index) => {
                const y = Math.floor(pos.y);
                if (y >= 0 && y < this.height) {
                    // Use different characters for trail effect
                    const trailChar = index === 0 ? drop.emoji : (index === 1 ? '.' : '·');
                    grid[y][pos.x] = trailChar;
                }
            });

            // Draw main emoji
            const y = Math.floor(drop.y);
            if (y >= 0 && y < this.height) {
                grid[y][drop.x] = drop.emoji;
            }
        });

        // Print grid
        console.log('╔' + '═'.repeat(this.width) + '╗');
        grid.forEach(row => {
            console.log('║' + row.join('') + '║');
        });
        console.log('╚' + '═'.repeat(this.width) + '╝');
        console.log(`🌧️  Emoji Rain  🌧️  [Press Ctrl+C to stop]`);
    }

    async run() {
        console.log('\n🎮 Starting Emoji Rain...\n');
        console.log('Get ready for the emoji shower! 🌈\n');
        
        // Wait a moment before starting
        await new Promise(resolve => setTimeout(resolve, 2000));

        const interval = setInterval(() => {
            this.update();
            this.render();
        }, 100);

        // Handle graceful exit
        process.on('SIGINT', () => {
            clearInterval(interval);
            console.clear();
            console.log('\n👋 Thanks for watching the emoji rain!\n');
            console.log('Stats:');
            console.log(`🌟 Total emojis spawned: ${this.drops.length}`);
            console.log(`🎯 Emoji variety: ${emojis.length} different types`);
            console.log('\nCome back anytime for more emoji fun! 🎉\n');
            process.exit(0);
        });
    }
}

// Bonus: Matrix-style emoji rain mode
class EmojiMatrix extends EmojiRain {
    constructor(width = 80, height = 20) {
        super(width, height);
        this.columns = [];
        for (let i = 0; i < width; i += 2) {
            this.columns.push({
                x: i,
                y: Math.random() * height,
                speed: Math.random() * 0.5 + 0.5,
                length: Math.floor(Math.random() * 10) + 5
            });
        }
    }

    update() {
        this.columns.forEach(col => {
            col.y += col.speed;
            if (col.y > this.height + col.length) {
                col.y = -col.length;
                col.speed = Math.random() * 0.5 + 0.5;
            }
        });
    }

    render() {
        console.clear();
        const grid = Array(this.height).fill(null).map(() => 
            Array(this.width).fill(' ')
        );

        this.columns.forEach(col => {
            for (let i = 0; i < col.length; i++) {
                const y = Math.floor(col.y - i);
                if (y >= 0 && y < this.height) {
                    const emoji = emojis[Math.floor(Math.random() * emojis.length)];
                    const brightness = i === 0 ? emoji : (i < 3 ? '○' : '·');
                    grid[y][col.x] = brightness;
                }
            }
        });

        console.log('╔' + '═'.repeat(this.width) + '╗');
        grid.forEach(row => {
            console.log('║' + row.join('') + '║');
        });
        console.log('╚' + '═'.repeat(this.width) + '╝');
        console.log(`🌐 Emoji Matrix 🌐  [Press Ctrl+C to stop]`);
    }
}

// Run the animation
if (require.main === module) {
    const mode = process.argv[2];
    
    console.log('🎨 Emoji Rain Animation 🎨\n');
    console.log('Usage: node emoji_rain.js [mode]');
    console.log('Modes: rain (default), matrix\n');
    
    const animation = mode === 'matrix' 
        ? new EmojiMatrix(60, 20)
        : new EmojiRain(60, 20);
    
    animation.run();
}