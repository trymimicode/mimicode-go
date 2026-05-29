const canvas = {
  width: 80,
  height: 20,
  particles: [],
  
  init() {
    // Create initial particles
    for (let i = 0; i < 15; i++) {
      this.particles.push({
        x: Math.random() * this.width,
        y: Math.random() * this.height,
        vx: (Math.random() - 0.5) * 2,
        vy: (Math.random() - 0.5) * 2,
        char: ['*', '·', '•', '◦', '○'][Math.floor(Math.random() * 5)],
        trail: []
      });
    }
  },
  
  update() {
    this.particles.forEach(p => {
      // Store previous position for trail
      p.trail.push({ x: p.x, y: p.y });
      if (p.trail.length > 5) p.trail.shift();
      
      // Update position
      p.x += p.vx;
      p.y += p.vy;
      
      // Bounce off walls
      if (p.x <= 0 || p.x >= this.width - 1) {
        p.vx *= -1;
        p.x = Math.max(0, Math.min(this.width - 1, p.x));
      }
      if (p.y <= 0 || p.y >= this.height - 1) {
        p.vy *= -1;
        p.y = Math.max(0, Math.min(this.height - 1, p.y));
      }
      
      // Add some randomness
      p.vx += (Math.random() - 0.5) * 0.1;
      p.vy += (Math.random() - 0.5) * 0.1;
      
      // Limit velocity
      p.vx = Math.max(-2, Math.min(2, p.vx));
      p.vy = Math.max(-1, Math.min(1, p.vy));
    });
  },
  
  render() {
    // Create empty grid
    const grid = Array(this.height).fill().map(() => 
      Array(this.width).fill(' ')
    );
    
    // Draw borders
    for (let x = 0; x < this.width; x++) {
      grid[0][x] = '─';
      grid[this.height - 1][x] = '─';
    }
    for (let y = 0; y < this.height; y++) {
      grid[y][0] = '│';
      grid[y][this.width - 1] = '│';
    }
    grid[0][0] = '┌';
    grid[0][this.width - 1] = '┐';
    grid[this.height - 1][0] = '└';
    grid[this.height - 1][this.width - 1] = '┘';
    
    // Draw particles and trails
    this.particles.forEach(p => {
      // Draw trail with fading effect
      p.trail.forEach((pos, i) => {
        const x = Math.round(pos.x);
        const y = Math.round(pos.y);
        if (x > 0 && x < this.width - 1 && y > 0 && y < this.height - 1) {
          grid[y][x] = '·';
        }
      });
      
      // Draw particle
      const x = Math.round(p.x);
      const y = Math.round(p.y);
      if (x > 0 && x < this.width - 1 && y > 0 && y < this.height - 1) {
        grid[y][x] = p.char;
      }
    });
    
    // Convert grid to string
    return grid.map(row => row.join('')).join('\n');
  },
  
  detectCollisions() {
    for (let i = 0; i < this.particles.length; i++) {
      for (let j = i + 1; j < this.particles.length; j++) {
        const p1 = this.particles[i];
        const p2 = this.particles[j];
        const dx = p1.x - p2.x;
        const dy = p1.y - p2.y;
        const dist = Math.sqrt(dx * dx + dy * dy);
        
        if (dist < 3) {
          // Simple elastic collision
          const tempVx = p1.vx;
          const tempVy = p1.vy;
          p1.vx = p2.vx;
          p1.vy = p2.vy;
          p2.vx = tempVx;
          p2.vy = tempVy;
          
          // Add explosion effect
          p1.char = '◉';
          p2.char = '◉';
          setTimeout(() => {
            p1.char = ['*', '·', '•', '◦', '○'][Math.floor(Math.random() * 5)];
            p2.char = ['*', '·', '•', '◦', '○'][Math.floor(Math.random() * 5)];
          }, 100);
        }
      }
    }
  }
};

// Animation loop
function animate() {
  console.clear();
  canvas.update();
  canvas.detectCollisions();
  console.log("✨ Particle Animation ✨\n");
  console.log(canvas.render());
  console.log(`\nParticles: ${canvas.particles.length} | Press Ctrl+C to exit`);
}

// Run the animation
canvas.init();
setInterval(animate, 100);