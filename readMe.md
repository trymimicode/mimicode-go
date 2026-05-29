# mimicode

[![CI](https://github.com/trymimicode/mimicode-go/actions/workflows/ci.yml/badge.svg)](https://github.com/trymimicode/mimicode-go/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Go Version](https://img.shields.io/badge/go-1.26-blue.svg)](https://go.dev/dl/)

A minimal coding agent for engineers who want to stay in control.

mimi does one thing: offloads the rote work — grepping through codebases, looking up docs,
running builds, making mechanical edits — so you can spend your time on the parts that
actually require a brain. It is not a pair programmer. It does not make decisions for you.
It is a tool, in the Unix sense.

Everything it does is logged. Every file it touches, every command it runs, every API call
it makes — written to a `.mimi/<session>.jsonl` file you can read, replay, or audit.
Memory is flat markdown in `.mimi/MEMORY.md`. No black boxes.

---

## Quick Start

```sh
# Install (requires Go 1.26+ and ripgrep)
curl -fsSL https://raw.githubusercontent.com/trymimicode/mimicode-go/main/install.sh | bash

# Set your API key
export ANTHROPIC_API_KEY="your-key-here"

# Run
mimicode "add tests to calc.go"
mimicode --tui  # terminal UI with streaming
```

---

## requirements

- [ripgrep](https://github.com/BurntSushi/ripgrep) (`rg`) — mimi uses it for all searches
- `ANTHROPIC_API_KEY` — Haiku and Sonnet via the Anthropic API

---

## installation

**Quick install** (requires Go 1.26+):
```sh
curl -fsSL https://raw.githubusercontent.com/trymimicode/mimicode-go/main/install.sh | bash
```

**Or install via Go**:
```sh
go install github.com/trymimicode/mimicode-go/cmd/mimicode@latest
```

**Or build from source**:
```sh
git clone https://github.com/trymimicode/mimicode-go.git
cd mimicode-go
make install
```

**Set your API key**:
```sh
export ANTHROPIC_API_KEY="your-key-here"
# Add to ~/.zshrc or ~/.bashrc to persist
```

---

## development

```sh
make build       # → ./mimicode
make install     # → $GOPATH/bin/mimicode
make test        # run all tests
make fmt         # format code
make vet         # run go vet
make dev         # build and run in TUI mode
make help        # show all targets
```

---

## usage

**one-shot** — pipe in a task, get output, back to your shell:
```sh
mimicode "why is this segfaulting"
mimicode "add a --dry-run flag to the CLI"
mimicode -s myfeature "continue from where we left off"
```

**REPL** — drop into a session:
```sh
mimicode
mimicode -s myfeature
```

**TUI** — Bubbletea interface with streaming output and markdown rendering:
```sh
mimicode --tui
mimicode --tui -s myfeature
```

Sessions are resumable. `-s <name>` names a session; omit it for an anonymous one.
Conversation history is saved under `~/.mimi/sessions/<id>/`.

---

## checkpoints & undo

Every turn that changes files is snapshotted into a shadow git repo (in the
session dir, work-tree pointed at your project). Your real `.git` is never
touched — no stray commits, no dirtied history.

```
> Add a Sub function to calc.go.
checkpoint eb33e84 — turn 1: Add a Sub function to calc.go.

> :undo            # revert the last turn
reverted to: session start

> :undo 3          # revert the last 3 turns
> :undo list       # show all checkpoints this session
```

`:undo` restores the working tree; it never deletes work irrecoverably
(reset commits stay reachable via `git reflog` in the shadow repo). Disable
with `MIMICODE_CHECKPOINT=0`.

---

## confirm-gate

Run with `--confirm` (or `MIMICODE_CONFIRM=1`) and mimi asks before every
side-effecting tool — `bash`, `write`, `edit`. Read-only tools (read, search,
web, memory) never prompt.

```sh
mimicode --confirm "refactor calc.go"
```
```
  mimi wants to run edit:
    calc.go
    - func Add(a, b int) int { return a + b }
    + func Add(a, b int) int { return a + b }
  allow? [y]es / [n]o:
```

Deny and mimi gets the refusal as a tool result — it won't retry the same call,
it picks a different approach or asks you what you want. Nothing touches your
files or shell without a `y`.

---

## self-recovery

When mimi gets stuck — repeating the same failing tool call, a run of errors,
or burning the step budget without finishing — it stops and diagnoses itself.
A fresh, clean model call reads the `events.jsonl` decision trace, works out
the root cause, and proposes a fix plus a durable rule. **It asks before acting**
— nothing is applied without your confirmation.

```
⚠ mimi got stuck.
  what went wrong: exhausted the step budget after editing but never ran the tests.
  recovery plan:   batch the test + vet into one command, then summarize.
  proposed rule:   For multi-step tasks, batch independent ops into one tool call.
  apply recovery? [y]es retry / [r]ule only / [n]o:
```

- **y** — append the rule to `.mimi/RULES.md`, checkpoint, reset to a *clean
  context* seeded only with the task + diagnosis, and retry.
- **r** — record the rule for next time, don't retry.
- **n** — do nothing.

It never rewrites its own code — only the markdown rules you can read and edit.
The clean-context retry runs once; if it sticks again, mimi reports and stops
rather than looping.

---

## tools

mimi has ten tools. That is the entire surface area.

| tool | what it does |
|---|---|
| `bash` | runs a shell command |
| `read` | reads a file with line numbers |
| `write` | creates or overwrites a file |
| `edit` | exact find-and-replace, atomic batch edits |
| `web_search` | DuckDuckGo search, supports `site:` filters |
| `web_fetch` | fetches a URL; handles GitHub issues, Reddit, HN, SO natively |
| `stackoverflow_search` | searches SO and returns questions + top answers inline |
| `memory_write` | appends a note to `.mimi/MEMORY.md` |
| `memory_search` | FTS5 search across sessions, memory, and rules |
| `recall_compaction` | loads a prior compaction summary |

mimi defaults to Sonnet. You pick the model — set `MIMICODE_MODEL` to override
(e.g. the cheaper Haiku). No hidden routing: the engineer decides, not a regex.

---

## audit trail

```
~/.mimi/sessions/<id>/
  events.jsonl        # every decision: model text, tool calls, tokens, ms
  messages.json       # full conversation for resume
  checkpoints.git/    # shadow repo backing :undo
  compactions.jsonl   # compaction summaries

<project>/.mimi/
  MEMORY.md           # cross-session notes (readable markdown)
  RULES.md            # behavioral rules learned from past sessions
```

The `events.jsonl` trace is the point: each step records the model's own text,
the tool calls it made with full inputs, the token split (in/out/cache), and
timing. You can replay exactly how mimi decided — nothing is hidden.

After each session, mimi runs a Haiku call to summarize what happened and appends
it to `MEMORY.md`. That summary is injected into the system prompt next time.
The loop adapts to your codebase and your workflow over time.

---

## config

| env var | default | description |
|---|---|---|
| `ANTHROPIC_API_KEY` | required | Anthropic API key |
| `MIMICODE_MODEL` | Sonnet | model id to use for every turn |
| `MIMICODE_CONFIRM` | `0` | set `1` to ask before each bash/write/edit (same as `--confirm`) |
| `MIMICODE_CHECKPOINT` | `1` | set `0` to disable turn checkpoints / `:undo` |
| `MIMICODE_MAX_STEPS` | `25` | max tool calls per turn |
| `MIMICODE_COMPACT_AUTO` | `true` | auto-compact long sessions |
| `MIMICODE_COMPACT_TURN_INTERVAL` | `5` | turns between compaction checks |
| `MIMICODE_COMPACT_TOKEN_THRESHOLD` | `20000` | token count that triggers compaction |
| `STACK_EXCHANGE_KEY` | optional | raises SO API quota from 300 to 10k req/day |

---

## origin

Started as a Python prototype at [mimicode](../mimicode). This is the Go port —
same architecture, same philosophy, statically compiled, no runtime dependencies
beyond `rg` and an API key.
