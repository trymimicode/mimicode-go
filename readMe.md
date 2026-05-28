# mimicode

A minimal coding agent for engineers who want to stay in control.

mimi does one thing: offloads the rote work — grepping through codebases, looking up docs,
running builds, making mechanical edits — so you can spend your time on the parts that
actually require a brain. It is not a pair programmer. It does not make decisions for you.
It is a tool, in the Unix sense.

Everything it does is logged. Every file it touches, every command it runs, every API call
it makes — written to a `.mimi/<session>.jsonl` file you can read, replay, or audit.
Memory is flat markdown in `.mimi/MEMORY.md`. No black boxes.

---

## requirements

- [ripgrep](https://github.com/BurntSushi/ripgrep) (`rg`) — mimi uses it for all searches
- `ANTHROPIC_API_KEY` — Haiku and Sonnet via the Anthropic API

---

## build

```sh
make build    # → ./mimicode
make install  # → $GOPATH/bin/mimicode
make test
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
Conversation history is saved to `.mimi/sessions/`.

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

mimi routes turns between Haiku (fast, cheap) and Sonnet (reasoning) based on
what the task actually needs. It never switches mid-turn — that would break prompt
cache and waste money.

---

## audit trail

```
.mimi/
  sessions/<id>.jsonl          # every action, timestamped
  sessions/<id>.messages.json  # full conversation for resume
  MEMORY.md                    # cross-session notes (readable markdown)
  RULES.md                     # behavioral rules learned from past sessions
```

After each session, mimi runs a Haiku call to summarize what happened and appends
it to `MEMORY.md`. That summary is injected into the system prompt next time.
The loop adapts to your codebase and your workflow over time.

---

## config

| env var | default | description |
|---|---|---|
| `ANTHROPIC_API_KEY` | required | Anthropic API key |
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
