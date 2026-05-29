## internal/provider — 2026-05-28T22:40:22Z
**Summary:** Added ModelOpus constant and changed default model from Sonnet to Opus
Updated provider.go to include claude-opus-4-20250514 as a model constant and made it the default model instead of Sonnet. The DefaultModel() function now returns ModelOpus unless overridden by MIMICODE_MODEL env var.
**Files:** internal/provider/provider.go
**Change:** : Added Opus 4 support and made it default (User requested to use Opus instead of Sonnet as the default model)

## internal/tui — 2026-05-28T23:05:00Z
**Summary:** Improved TUI tool call display by moving it to a dedicated status bar above the ribbon
**Files:** internal/tui/tui.go

## internal/tui — 2026-05-28T23:16:19Z
**Summary:** Enhanced TUI with multiline input, word wrapping, and color-coded diff display
**Files:** internal/tui/tui.go

## internal/tui — 2026-05-28T23:28:49Z
**Summary:** Fixed stuck loader, added Shift+Enter newline, onboarding screen, and enhanced text input features
**Files:** internal/tui/tui.go

## test — 2026-05-29T00:05:44Z
**Summary:** Created test suite with 6 Python files containing intentional bugs and incomplete features for testing AI capabilities
**Files:** test/calculator.py, test/test_calculator.py, test/todo_list.py, test/web_scraper.py, test/fibonacci.py, test/README.md

## test — 2026-05-29T00:24:39Z
**Summary:** Created test folder with 4 code files (hello.py, calculator.go, game.js, ascii_art.py) and edited hello.py to add multilingual support
**Files:** test/hello.py, test/calculator.go, test/game.js, test/ascii_art.py

