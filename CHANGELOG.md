# Changelog

All notable changes to mimicode will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- CI/CD workflows for automated testing and releases
- One-click install script with binary download support
- Enhanced `--version` flag showing build info (commit, date, Go version)
- Contributing guidelines and troubleshooting documentation
- Per-turn checkpoints with `:undo` command via shadow git repo
- Self-recovery system that diagnoses stuck turns and proposes fixes
- `--confirm` gate to ask before executing bash/write/edit commands
- TUI improvements: diff rendering, onboarding, multi-line input
- Support for Claude Opus 4 model

### Changed
- Improved TUI with better keyboard navigation (arrow keys, home/end)
- Enhanced install script to detect system and download appropriate binary
- Better error messages and health checks

### Fixed
- TUI stuck loader issue
- Various stability improvements in agent loop

## [0.1.0] - 2026-05-29

### Added
- Initial release of mimicode-go
- Core agent loop with 10 tools (bash, read, write, edit, web, memory, etc.)
- Session management with resumable conversations
- Conversation compaction for long sessions
- Memory and rules system for learning across sessions
- Terminal UI (TUI) with streaming output
- REPL and one-shot modes
- Audit trail with detailed event logging
- Integration with Anthropic Claude API (Haiku, Sonnet, Opus)

[unreleased]: https://github.com/trymimicode/mimicode-go/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/trymimicode/mimicode-go/releases/tag/v0.1.0
