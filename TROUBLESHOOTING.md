# Troubleshooting Guide

## Installation Issues

### "command not found: mimicode"

**Cause:** The installation directory is not in your PATH.

**Fix:**
```bash
# Check if ~/.local/bin is in your PATH
echo $PATH

# If not, add to your shell profile (~/.bashrc, ~/.zshrc, etc.):
export PATH="$HOME/.local/bin:$PATH"

# Then reload your shell:
source ~/.bashrc  # or ~/.zshrc
```

### "permission denied" when running mimicode

**Cause:** Binary is not executable.

**Fix:**
```bash
chmod +x ~/.local/bin/mimicode
```

### Installation fails: "No pre-built binary available"

**Cause:** GitHub release doesn't have binaries yet, or unsupported platform.

**Fix:** Install Go 1.26+ and the installer will build from source automatically:
```bash
# macOS
brew install go

# Linux (Ubuntu/Debian)
sudo apt install golang-go

# Then re-run installer
curl -fsSL https://raw.githubusercontent.com/trymimicode/mimicode-go/main/install.sh | bash
```

---

## Runtime Issues

### "ANTHROPIC_API_KEY is not set"

**Cause:** Missing API key environment variable.

**Fix:**
```bash
# Get your key from: https://console.anthropic.com/settings/keys

# Set temporarily (current session only):
export ANTHROPIC_API_KEY="sk-ant-..."

# Set permanently - add to ~/.bashrc or ~/.zshrc:
echo 'export ANTHROPIC_API_KEY="sk-ant-..."' >> ~/.bashrc
source ~/.bashrc
```

### "ripgrep (rg) not found"

**Cause:** mimicode requires ripgrep for file searching.

**Fix:**
```bash
# macOS
brew install ripgrep

# Linux (Ubuntu/Debian)
sudo apt install ripgrep

# Linux (Fedora/RHEL)
sudo dnf install ripgrep

# Or download from: https://github.com/BurntSushi/ripgrep
```

### API Rate Limit / 429 errors

**Cause:** Anthropic API rate limits exceeded.

**Symptoms:**
- "rate_limit_error" in output
- Requests failing intermittently

**Fix:**
- Wait a few seconds and retry
- Check your API plan limits at https://console.anthropic.com
- Consider upgrading your plan for higher limits

### "Agent got stuck" / Exhausted step budget

**Cause:** Agent hit max tool call limit (default 25 per turn).

**Fix:**
- Let the self-recovery system diagnose and propose a fix
- If in one-shot mode, switch to REPL/TUI for recovery
- Increase limit: `export MIMICODE_MAX_STEPS=50`
- Break down your request into smaller steps

---

## Session Issues

### Can't resume session: "session not found"

**Cause:** Session ID doesn't exist or wrong directory.

**Check sessions:**
```bash
ls ~/.mimi/sessions/
```

**Fix:**
- Verify the session ID is correct
- Sessions are stored in `~/.mimi/sessions/<id>/`
- If deleted, start a new session

### Session files corrupted

**Symptoms:**
- JSON parse errors
- "invalid message format"

**Fix:**
```bash
# Backup the session
cp -r ~/.mimi/sessions/<id> ~/.mimi/sessions/<id>.backup

# Try deleting corrupted files:
rm ~/.mimi/sessions/<id>/messages.json

# Restart session - it will rebuild from events.jsonl
mimicode -s <id>
```

### Undo doesn't work: "no checkpoints found"

**Cause:** Checkpoints disabled or session too old.

**Check:**
```bash
# Verify checkpoints enabled (default):
echo $MIMICODE_CHECKPOINT  # should be empty or "1"

# Check shadow repo exists:
ls ~/.mimi/sessions/<current-session>/checkpoints.git/
```

**Fix:**
- Ensure `MIMICODE_CHECKPOINT` is not set to `0`
- Checkpoints only work for sessions created with checkpoint support
- Old sessions need fresh start for undo support

---

## Performance Issues

### Slow response times

**Cause:** Multiple factors.

**Debug:**
```bash
# Check API latency - look for timing in events:
cat ~/.mimi/sessions/<id>/events.jsonl | grep ms

# Check if compaction is happening too often:
export MIMICODE_COMPACT_TURN_INTERVAL=10  # default: 5
```

### High token usage / costs

**Symptoms:**
- Large API bills
- Frequent compaction

**Fix:**
```bash
# Use cheaper model:
export MIMICODE_MODEL=claude-3-5-haiku-20241022

# Reduce compaction frequency:
export MIMICODE_COMPACT_AUTO=false

# Check current usage:
cat ~/.mimi/sessions/<id>/events.jsonl | grep '"usage"'
```

---

## TUI Issues

### TUI displays garbled/broken characters

**Cause:** Terminal doesn't support required features.

**Fix:**
- Use a modern terminal (iTerm2, Alacritty, Wezterm)
- Ensure UTF-8 encoding: `export LANG=en_US.UTF-8`
- Try non-TUI mode: `mimicode` (REPL) or one-shot

### Can't exit TUI

**Fix:**
- Press `Ctrl+C` or `Ctrl+D`
- If frozen, press `Ctrl+Z` then `kill %1`

### Streaming output stuck / not updating

**Fix:**
- This was fixed in recent versions - update:
  ```bash
  curl -fsSL https://raw.githubusercontent.com/trymimicode/mimicode-go/main/install.sh | bash
  ```

---

## Development / Build Issues

### "go: no such tool: covdata"

**Cause:** Older Go version or missing coverage tools.

**Fix:**
```bash
# Update Go to 1.26+:
go version  # check current version

# Or run tests without coverage:
go test -v ./...
```

### Tests fail with race detector errors

**Cause:** Actual race conditions (rare) or false positives.

**Fix:**
```bash
# Run without race detector:
make test-short

# Or:
go test -v ./...
```

---

## Getting Help

If none of the above help:

1. **Check the logs:**
   ```bash
   # Session events:
   cat ~/.mimi/sessions/<id>/events.jsonl

   # Latest session:
   ls -lt ~/.mimi/sessions/ | head -5
   ```

2. **Run health check:**
   ```bash
   # Check dependencies:
   which rg
   echo $ANTHROPIC_API_KEY
   mimicode --version
   ```

3. **File an issue:**
   - Include: OS, Go version (`go version`), mimicode version
   - Attach: relevant section of `events.jsonl`
   - Describe: steps to reproduce
   - GitHub: https://github.com/trymimicode/mimicode-go/issues

4. **Enable debug logging:**
   ```bash
   # (Future feature - for now, check events.jsonl)
   ```

---

## Common Mistakes

### Using `cat` on source files instead of `read` tool

**Wrong:**
```bash
mimicode "cat main.go"
```

**Right:**
```bash
mimicode "show me main.go"
# mimicode will use the 'read' tool internally
```

### Expecting it to make decisions for you

**Remember:** mimicode is a tool, not a pair programmer. It:
- Does rote work (searching, editing, running commands)
- Doesn't make architectural decisions
- Asks when stuck or needs direction

### Not using sessions for multi-step work

**One-shot is for quick tasks:**
```bash
mimicode "add a test to calc.go"  # ✓ good
```

**Use sessions for complex work:**
```bash
mimicode -s refactor  # start or resume
> Refactor the auth module
> Now add tests
> Run the tests
```

---

**Last updated:** 2026-05-29
