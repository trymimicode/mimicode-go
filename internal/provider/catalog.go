package provider

// This file exposes a small, stable catalog of providers and models so the TUI
// can present them in pickers without scraping the package's internals. The
// single source of truth for the canonical model constants (ModelHaiku,
// ModelSonnet, ModelOpus) and provider vars (Claude, Kimi, MiniMax) stays in
// claude.go and openai.go — this file just describes them.

// ProviderMeta describes a provider for picker UIs. Prov is the callable to
// assign to AgentConfig.Provider; EnvVar is the env var the user must export
// (or save via the key-prompt flow) before this provider can be used. The empty
// string for Claude reflects the fact that ANTHROPIC_API_KEY is required by
// the agent regardless of the user's /provider choice.
type ProviderMeta struct {
	ID     string   // "claude", "kimi", "minimax"
	Label  string   // "Claude", "Kimi", "MiniMax"
	Prov   Provider // nil for Claude
	EnvVar string   // "" for Claude, "MOONSHOT_API_KEY" for Kimi, etc.
}

// ModelMeta describes a single model across all providers. ID is a short slug
// suitable for display ("haiku", "kimi"); FullName is what callers pass to the
// API. Provider identifies the backend the model lives on.
type ModelMeta struct {
	ID       string       // short slug
	FullName string       // API model name
	Provider ProviderMeta // owning provider
}

// Providers returns the catalog of providers in picker order.
func Providers() []ProviderMeta {
	return []ProviderMeta{
		{ID: "claude", Label: "Claude", Prov: Claude, EnvVar: ""},
		{ID: "kimi", Label: "Kimi", Prov: Kimi, EnvVar: "MOONSHOT_API_KEY"},
		{ID: "minimax", Label: "MiniMax", Prov: MiniMax, EnvVar: "MINIMAX_API_KEY"},
	}
}

// Models returns the flat list of all models across all providers, in picker
// order. Each provider's models appear under it (claude first, then kimi,
// then minimax). Kimi and MiniMax each expose a single row — their
// DefaultModel() — since the openai-compatible backends don't enumerate a
// model list to us.
func Models() []ModelMeta {
	var out []ModelMeta
	for _, pm := range Providers() {
		switch pm.ID {
		case "claude":
			out = append(out,
				ModelMeta{ID: "haiku", FullName: ModelHaiku, Provider: pm},
				ModelMeta{ID: "sonnet", FullName: ModelSonnet, Provider: pm},
				ModelMeta{ID: "opus", FullName: ModelOpus, Provider: pm},
			)
		case "kimi":
			out = append(out, ModelMeta{ID: "kimi", FullName: Kimi.DefaultModel(), Provider: pm})
		case "minimax":
			out = append(out, ModelMeta{ID: "minimax", FullName: MiniMax.DefaultModel(), Provider: pm})
		}
	}
	return out
}
