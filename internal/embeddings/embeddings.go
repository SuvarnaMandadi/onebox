// Package embeddings defines the provider interface the RAG engine embeds
// text through, plus OpenAI-compatible and Ollama adapters. Kept small and
// swappable per the blueprint's anti-scope: onebox calls providers, it
// does not train or run its own models.
package embeddings

import "context"

// Provider turns text into vectors. Implementations call an external API
// (or a local Ollama daemon) — onebox never runs a model itself.
type Provider interface {
	// Embed returns one vector per input text, in the same order.
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}
