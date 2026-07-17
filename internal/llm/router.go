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
	switch ProviderKind(model) {
	case "anthropic":
		if r.Anthropic == nil {
			return nil, fmt.Errorf("no Anthropic API key configured (set ONEBOX_ANTHROPIC_API_KEY)")
		}
		return r.Anthropic, nil
	case "openai":
		if r.OpenAI == nil {
			return nil, fmt.Errorf("no OpenAI API key configured (set ONEBOX_OPENAI_API_KEY)")
		}
		return r.OpenAI, nil
	default:
		if r.Ollama == nil {
			return nil, fmt.Errorf("no Ollama backend configured (set ONEBOX_OLLAMA_BASE_URL or run Ollama on its default port)")
		}
		return r.Ollama, nil
	}
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
