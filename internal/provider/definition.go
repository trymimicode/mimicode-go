package provider

import "context"

// Provider is the callable abstraction for an LLM backend.
// Mirrors opencode's Definition: a fixed identity and a callable.
// Each backend (Claude, OpenAI-compatible) implements this interface.
type Provider interface {
	Call(ctx context.Context, messages []Message, system string, tools []ToolSchema, model string) (Message, Usage, error)
	CallStreaming(ctx context.Context, messages []Message, system string, tools []ToolSchema, model string, cb StreamCallback) (Message, Usage, error)
	DefaultModel() string
}
