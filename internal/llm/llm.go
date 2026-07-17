// Package llm provides a provider-agnostic chat-completion gateway:
// Anthropic, OpenAI, and Ollama adapters behind one interface, so
// /api/llm/chat and the RAG engine's /api/rag/answer both just send
// {model, messages} without caring which backend serves it.
package llm

import "context"

type Message struct {
	Role    string `json:"role"` // "system", "user", or "assistant"
	Content string `json:"content"`
}

type ChatRequest struct {
	Model    string
	Messages []Message
}

// ChatResult carries token counts alongside the answer so callers (usage
// logging, spend caps) don't need provider-specific response parsing.
type ChatResult struct {
	Content   string
	TokensIn  int
	TokensOut int
}

// Provider is a single chat-completion backend.
type Provider interface {
	Chat(ctx context.Context, req ChatRequest) (ChatResult, error)
	// ChatStream calls onDelta with each incremental text fragment as it
	// arrives, and still returns the full accumulated ChatResult at the
	// end (with usage), so callers don't have to reassemble it themselves.
	ChatStream(ctx context.Context, req ChatRequest, onDelta func(string)) (ChatResult, error)
}
