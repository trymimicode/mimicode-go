# Contributing to mimicode

Thank you for considering contributing to mimicode!

## Quick Start

```sh
# Clone the repository
git clone https://github.com/trymimicode/mimicode-go.git
cd mimicode-go

# Build and test
make build
make test

# Run in dev mode
make dev
```

## Prerequisites

- Go 1.26+
- [ripgrep](https://github.com/BurntSushi/ripgrep) (`rg`)
- `ANTHROPIC_API_KEY` environment variable

## Development Workflow

1. **Fork** the repository
2. **Create a branch**: `git checkout -b feature/my-feature`
3. **Make your changes** and add tests
4. **Run tests**: `make test`
5. **Format code**: `make fmt`
6. **Vet code**: `make vet`
7. **Commit** with a clear message
8. **Push** and open a Pull Request

## Code Style

- Follow standard Go conventions
- Run `gofmt` before committing (or use `make fmt`)
- Keep functions focused and small
- Add comments for exported functions
- Write tests for new functionality

## Testing

```sh
make test        # run all tests with race detector
make test-short  # faster tests without race detector
make coverage    # generate coverage report
```

All PRs should maintain or improve test coverage.

## Project Structure

```
mimicode-go/
├── cmd/mimicode/         # CLI entry point
├── internal/
│   ├── agent/            # Core agent loop, tool dispatch
│   ├── provider/         # Anthropic API client
│   ├── tools/            # Tool implementations (bash, read, edit, etc.)
│   ├── memory/           # Memory & search
│   ├── store/            # Session persistence
│   ├── compactor/        # Conversation compaction
│   ├── checkpoint/       # Undo/redo via shadow git
│   ├── recovery/         # Self-diagnosis
│   ├── repomap/          # Code symbol extraction
│   ├── reflect/          # Post-turn reflection
│   └── tui/              # Bubbletea TUI
├── Makefile
└── install.sh
```

## Pull Request Guidelines

- **One feature per PR** — keeps review focused
- **Write tests** for new functionality
- **Update documentation** if you change behavior
- **Keep commits clean** — squash if needed
- **Link issues** if your PR fixes a bug

## Reporting Issues

When filing an issue, please include:

- **OS and Go version**: `go version`
- **Steps to reproduce**
- **Expected vs actual behavior**
- **Session logs** if relevant (`.mimi/sessions/<id>/events.jsonl`)

## Questions?

Open an issue or discussion. We're happy to help!

---

## Philosophy

mimicode is a **tool**, not a pair programmer. It:

- Does the rote work (searching, editing, running commands)
- Stays transparent (everything logged to `.jsonl`)
- Lets the engineer decide (no magic, no hidden routing)
- Learns over time (memory, rules, compaction)

Contributions should align with this philosophy. If unsure, open an issue first to discuss.
