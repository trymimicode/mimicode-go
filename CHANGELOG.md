# Changelog

All notable changes to mimicode will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.6.0] - 2026-06-12

### Added
- Version number now displayed in the TUI
- Support contact email added to documentation

### Fixed
- Streaming render race condition and multi-step tail append bug
- Missing brace in `handleKey` session-browser block
- Color rendering and user scroll issues in TUI
- Repetition error and highlight color regression in TUI

### Changed
- TUI and provider: added picker UIs and automatic retry on stream drop
- Streaming now renders in real-time instead of after stream completion
- Re-applied multi-provider support after a revert cycle resolved merge conflicts

## [0.5.0] - 2026-06-09

### Added
- Multi-provider support (OpenAI-compatible API)
- Session browser with full-screen picker UI
- Session auto-naming: sessions are renamed to a 3–4 word hyphenated slug after the first turn
- Session management: sorting, model display, and persistence fixes
- TUI: syntax highlighting in diff view
- TUI: formatting for reading and diff panels
- TUI: scroll support
- TUI: clickable file path references — click a path in chat to open it in diff view
- TUI: `@filename` reference resolution
- TUI: mouse scroll routing in diff panel with navigate-mode hint
- TUI: text selection with clipboard copy
- TUI: bash tool output shown as inline feedback
- TUI: multi-line paste support
- Memory: bounded injection with incremental index and retrieval
- Language packs and project conventions downloading
- API key setup guide in `mimicode help` output and install script

### Fixed
- Error handling and rate-limit recovery for multi-step agent runs
- Keybind display rendering in TUI
- Dead Go code and untracked `.mimi` files removed

### Changed
- Streaming hardened; system prompt split into focused sections; global rules applied per turn

## [0.4.0] - 2026-06-01

### Added
- Prebuilt binary download for Windows AMD64 and ARM64 in install scripts
- Force prompt execution toggle for the agent harness
- Agent: loads project `AGENTS.md` / `CLAUDE.md` into the system prompt automatically
- Agent: rewrote system prompt around a plan → edit → verify loop
- Agent: hardened edit parsing and enriched tool schemas

### Fixed
- Install scripts for both PowerShell and Bash
- Stored snapshot error causing watch-mode state corruption
- API key header conflict with string quoting
- Watch state rewrite to fix race conditions under load

### Changed
- `--version` flag no longer reads env or config — instant and safe
- Provider API request modernized to current Anthropic spec
- `watch.go` core loop rewritten with error handling and full test coverage

## [0.3.0] - 2026-05-30

### Added
- Windows CI workflow in GitHub Actions

### Fixed
- Windows CI failure caused by `-coverprofile=coverage.out` being split by PowerShell

## [0.2.0] - 2026-05-30

### Added
- `mimicode key` command to view and set the API key
- `mimicode repl` as an explicit sub-command (REPL no longer starts automatically)
- Ambient file watcher: agent reads and responds to a `code.mimi` file
- `git_source` tool for cloning and reading real library source
- Diff viewer and command injector in TUI
- Per-turn checkpoints with `:undo` command via a shadow git repo
- Self-recovery loop: diagnoses stuck turns from the event log and proposes rules
- `--confirm` gate: prompts before executing `bash`, `write`, or `edit` tools
- TUI: markdown rendering for assistant messages via glamour
- TUI: improved keyboard navigation, diff rendering, and onboarding flow
- Support for Claude Opus model
- CI/CD workflows with binary download install support
- Troubleshooting guide and improved README
- `web_search`, `web_fetch`, and `stackoverflow_search` tools

### Fixed
- `bash` tool in `code.mimi` watcher broken by hardcoded `/bin/sh` path
- TUI diff view scroll lag
- DuckDuckGo redirect URLs now decoded correctly
- GitHub issue HTML stripped before injection into context
- Duplicate session line in stored context

### Changed
- Refactored internals: replaced logger/session/router with unified `store` package
- Repomap cache now refreshes asynchronously
- Rich decision trace added to event log

## [0.1.0] - 2026-05-10

### Added
- Initial release of mimicode-go
- Core agentic loop: call Claude → dispatch tools → append results → repeat until done
- Ten built-in tools: `bash`, `read`, `write`, `edit`, `web_search`, `web_fetch`, `stackoverflow_search`, `git_source`, memory read/write
- Session management with resumable conversations (JSON sidecar alongside JSONL event log)
- Conversation compaction: summarises old turns when context exceeds token or turn threshold
- Memory and rules system: agent writes to memory markdown; injected into every system prompt
- Lightweight repomap: AST scanner builds a file → symbols map injected into system prompts
- Terminal UI (TUI) with streaming output and diff rendering
- REPL and one-shot (`-p`) modes
- Post-session reflection: Haiku call that summarises the session and writes to `MEMORY.md`
- Append-only JSONL audit trail with per-event structured logging
- Integration with Anthropic Claude API (Haiku, Sonnet, Opus)

[unreleased]: https://github.com/trymimicode/mimicode-go/compare/v0.6...HEAD
[0.6.0]: https://github.com/trymimicode/mimicode-go/compare/v0.5...v0.6
[0.5.0]: https://github.com/trymimicode/mimicode-go/compare/v0.4...v0.5
[0.4.0]: https://github.com/trymimicode/mimicode-go/compare/v0.3...v0.4
[0.3.0]: https://github.com/trymimicode/mimicode-go/compare/v0.2...v0.3
[0.2.0]: https://github.com/trymimicode/mimicode-go/compare/v0.1...v0.2
[0.1.0]: https://github.com/trymimicode/mimicode-go/releases/tag/v0.1
