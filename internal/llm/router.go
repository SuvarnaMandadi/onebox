package llm

import (
	"context"
	"fmt"
	"strings"
)

// Router picks a Provider from a chat model's name prefix, so callers
// only ever send {model, messages} — matching the blueprint's
// provider-agnostic /api/llm/chat contract — without a separate
// "provider" field. Any sub-provider left nil (no API key configured)
// surfaces as a clear per-request error instead of a nil dereference.
type Router struct {
	Anthropic Provider
	OpenAI    Provider
	Ollama    Provider
}

// ProviderKind returns which provider a model name would route to:
// "anthropic", "openai", or "ollama". Exported so callers (e.g. usage
// logging) can label a request's provider without duplicating the
// prefix rules.
func ProviderKind(model string) string {
	lower := strings.ToLower(model)
	switch {
	case strings.HasPrefix(lower, "claude"):
		return "anthropic"
	case strings.HasPrefix(lower, "gpt") || strings.HasPrefix(lower, "o1") || strings.HasPrefix(lower, "o3") || strings.HasPrefix(lower, "text-"):
		return "openai"
	default:
		// Local/community model names have no fixed scheme, so anything
		// that isn't a recognized Anthropic/OpenAI prefix is assumed to
		// be an Ollama model.
		return "ollama"
	}
}

func (r *Router) providerFor(model string) (Provider, error) {
	return r.Named(ProviderKind(model))
}

// Named returns the Provider for an explicit provider kind — "anthropic",
// "openai", or "ollama" — with no model-name guessing involved. This is
// what callers who already know which provider they want (the dashboard's
// configured Chat Provider, driving the admin chatbot and /api/rag/answer)
// should use instead of providerFor/ProviderKind's prefix-based inference,
// which exists only to keep POST /api/llm/chat's "just send a model name"
// contract working for direct API callers that never state a provider.
func (r *Router) Named(kind string) (Provider, error) {
	switch kind {
	case "anthropic":
		if r.Anthropic == nil {
			return nil, fmt.Errorf("no Anthropic API key configured (set it in Settings, or ONEBOX_ANTHROPIC_API_KEY)")
		}
		return r.Anthropic, nil
	case "openai":
		if r.OpenAI == nil {
			return nil, fmt.Errorf("no OpenAI API key configured (set it in Settings, or ONEBOX_OPENAI_API_KEY)")
		}
		return r.OpenAI, nil
	case "ollama":
		if r.Ollama == nil {
			return nil, fmt.Errorf("no Ollama backend configured (set ONEBOX_OLLAMA_BASE_URL or run Ollama on its default port)")
		}
		return r.Ollama, nil
	default:
		return nil, fmt.Errorf("unknown chat provider %q", kind)
	}
}

// ChatWithProvider and ChatStreamWithProvider dispatch to an explicit
// provider kind (see Named) rather than guessing one from req.Model —
// used by call sites that already resolved a configured Chat Provider.
func (r *Router) ChatWithProvider(ctx context.Context, kind string, req ChatRequest) (ChatResult, error) {
	p, err := r.Named(kind)
	if err != nil {
		return ChatResult{}, err
	}
	return p.Chat(ctx, req)
}

func (r *Router) ChatStreamWithProvider(ctx context.Context, kind string, req ChatRequest, onDelta func(string)) (ChatResult, error) {
	p, err := r.Named(kind)
	if err != nil {
		return ChatResult{}, err
	}
	return p.ChatStream(ctx, req, onDelta)
}

func (r *Router) Chat(ctx context.Context, req ChatRequest) (ChatResult, error) {
	p, err := r.providerFor(req.Model)
	if err != nil {
		return ChatResult{}, err
	}
	return p.Chat(ctx, req)
}

func (r *Router) ChatStream(ctx context.Context, req ChatRequest, onDelta func(string)) (ChatResult, error) {
	p, err := r.providerFor(req.Model)
	if err != nil {
		return ChatResult{}, err
	}
	return p.ChatStream(ctx, req, onDelta)
}
