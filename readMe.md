# mimicode

A quiet coding agent for engineers who want to stay sharp.

mimi handles the rote work — searching your codebase, reading docs, running builds, making mechanical edits — so you can spend your energy on the parts that actually require a brain.

It is not a pair programmer. It does not make decisions for you. It is a tool, in the Unix sense.

---

## install

```sh
curl -fsSL https://raw.githubusercontent.com/trymimicode/mimicode-go/main/install.sh | bash
```

or via Go:

```sh
go install github.com/trymimicode/mimicode-go/cmd/mimicode@latest
```

**requirements:** [ripgrep](https://github.com/BurntSushi/ripgrep) and an `ANTHROPIC_API_KEY`.

---

## use

```sh
mimicode "why is this segfaulting"
mimicode "add a --dry-run flag"
mimicode --tui                     # terminal UI with streaming
mimicode -s myfeature "continue"   # named, resumable session
```

---

## how it works

mimi has ten tools. that is the entire surface area.

| tool | what it does |
|---|---|
| `bash` | runs a shell command |
| `read` | reads a file with line numbers |
| `write` | creates or overwrites a file |
| `edit` | exact find-and-replace, atomic batch edits |
| `web_search` | DuckDuckGo, supports `site:` filters |
| `web_fetch` | fetches a URL; handles GitHub, Reddit, HN, SO natively |
| `stackoverflow_search` | SO questions + top answers inline |
| `memory_write` | appends a note to `.mimi/MEMORY.md` |
| `memory_search` | full-text search across sessions, memory, and rules |
| `recall_compaction` | loads a prior session summary |

---

## nothing is hidden

every file touched, every command run, every API call — written to a `.mimi/<session>.jsonl` file you can read, replay, or audit. memory lives in flat markdown. no black boxes.

```
~/.mimi/sessions/<id>/
  events.jsonl      # every decision: model text, tool calls, tokens, timing
  messages.json     # full conversation for resume

<project>/.mimi/
  MEMORY.md         # cross-session notes
  RULES.md          # behavioral rules learned from past sessions
```

---

## undo

every turn that changes files is snapshotted. your real `.git` is never touched.

```sh
> :undo            # revert the last turn
> :undo 3          # revert the last 3 turns
> :undo list       # show all checkpoints
```

disable with `MIMICODE_CHECKPOINT=0`.

---

## confirm mode

run with `--confirm` and mimi asks before every write, edit, or shell command. read-only tools never prompt.

```sh
mimicode --confirm "refactor calc.go"
```

---

## when it gets stuck

if mimi repeats itself, burns the step budget, or hits a run of errors — it stops, reads its own trace, and explains what went wrong. it proposes a fix and asks before doing anything. it never retries silently.

---

## codebase

44 Go files across 13 packages. readable in one sitting.

```
cmd/mimicode/        — entry points (watch, tui, key)
internal/
  agent/             — turn loop, tool dispatch, stuck detection
  provider/          — Anthropic API calls, streaming
  tools/             — bash, read, write, edit, web, git
  tui/               — bubbletea terminal UI
  watch/             — notebook/file watcher mode
  store/             — session persistence (sqlite)
  memory/            — cross-session memory and FTS search
  reflect/           — post-session summarization
  recovery/          — stuck diagnosis
  checkpoint/        — shadow git for undo
  compactor/         — long-session compaction
  repomap/           — symbol index
  gitsource/         — git clone for library inspection
```

---

## config

| env var | default | description |
|---|---|---|
| `ANTHROPIC_API_KEY` | required | Anthropic API key |
| `MIMICODE_MODEL` | claude-opus-4 | model to use |
| `MIMICODE_THINKING` | `medium` | thinking budget: `off` / `low` / `medium` / `high` |
| `MIMICODE_CONFIRM` | `0` | `1` to confirm before each write/edit/bash |
| `MIMICODE_CHECKPOINT` | `1` | `0` to disable undo |
| `MIMICODE_MAX_STEPS` | `25` | max tool calls per turn |

---

## build from source

```sh
git clone https://github.com/trymimicode/mimicode-go.git
cd mimicode-go
make install
```

```sh
make build    # → ./mimicode
make test     # run all tests
make fmt      # format
make vet      # vet
```

---

MIT license.
