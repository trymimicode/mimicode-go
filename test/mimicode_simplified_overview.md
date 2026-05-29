# Mimicode Go - Simplified Flow Diagram

## User Interaction Flow
```
User Input → CLI/TUI Interface → Agent Core → Claude API
                                      ↓
                              Tool Execution Loop
                                      ↓
                         [bash, read, write, edit, web_search, etc.]
                                      ↓
                              Response Generation
                                      ↓
                            Display to User + Save Session
```

## Key Data Structures

### Message Format
```
Message {
    Role: "user" | "assistant"
    Content: [
        {Type: "text", Text: "..."},
        {Type: "tool_use", ID: "...", Name: "...", Input: {...}},
        {Type: "tool_result", Content: "...", IsError: bool}
    ]
}
```

### Session Storage
```
~/.mimi/sessions/
    └── <session-id>/
        ├── events.jsonl     # Append-only event log
        ├── messages.json    # Full conversation
        └── meta.json        # Session metadata
```

### Memory Storage
```
~/.mimi/
    ├── MEMORY.md           # Accumulated learnings
    ├── RULES.md            # Custom instructions
    └── memory.db           # SQLite FTS5 index
```

## Core Algorithm Loop

```
1. User provides prompt
2. Load session context (messages, memory, repo map)
3. While not done and steps < MAX_STEPS:
   a. Call Claude with current context
   b. If response has tool calls:
      - Execute each tool
      - Add results to context
      - Continue loop
   c. If response is text only:
      - Display to user
      - Break loop
4. Save session
5. Maybe compact old messages
6. Maybe reflect and update memory
```

## Tool Safety Features

- **Bash**: Blocks dangerous commands (rm -rf /, curl|sh, etc.)
- **Read**: Prevents binary files, enforces size limits
- **Edit**: Requires exact match, atomic updates
- **Web**: Truncates large responses, handles special sites

## Unique Features

1. **Automatic Compaction**: Summarizes old conversations to stay within token limits
2. **Reflection System**: AI reviews its work and saves learnings
3. **Repository Understanding**: Builds symbol map for better code navigation
4. **Streaming TUI**: Real-time display with syntax highlighting
5. **Memory Search**: Full-text search across all past sessions

## Models Used

- **Primary**: Claude Opus 4 (default for main interactions)
- **Compaction**: Claude Haiku 4.5 (fast/cheap for summarization)
- **Override**: MIMICODE_MODEL environment variable

## Error Handling Philosophy

- Tool errors are returned as normal results (not exceptions)
- Agent interprets errors and decides next steps
- User can interrupt with Ctrl+C at any time
- Sessions auto-save after each turn