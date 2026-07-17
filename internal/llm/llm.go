// Package llm provides a minimal chat-completion client used by the RAG
// engine's /api/rag/answer endpoint. This is intentionally small: the
// full provider-agnostic gateway (Anthropic/OpenAI/Ollama adapters,
// streaming, caching, rate limits, usage logging) is Month 4 scope. RAG
// just needs to turn retrieved context into an answer.
package llm

import "context"

// ChatClient completes a single system+user prompt into a text answer.
type ChatClient interface {
	Complete(ctx context.Context, systemPrompt, userPrompt string) (string, error)
}
