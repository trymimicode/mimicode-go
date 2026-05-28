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
