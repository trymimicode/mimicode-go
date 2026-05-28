## internal/provider — 2026-05-28T22:40:22Z
**Summary:** Added ModelOpus constant and changed default model from Sonnet to Opus
Updated provider.go to include claude-opus-4-20250514 as a model constant and made it the default model instead of Sonnet. The DefaultModel() function now returns ModelOpus unless overridden by MIMICODE_MODEL env var.
**Files:** internal/provider/provider.go
**Change:** : Added Opus 4 support and made it default (User requested to use Opus instead of Sonnet as the default model)

