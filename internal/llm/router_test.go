package llm

import (
	"context"
	"strings"
	"testing"
)

// stubProvider is a minimal llm.Provider that just tags its ChatResult
// with a name, so tests can assert *which* provider actually handled a
// call without needing a real HTTP backend.
type stubProvider struct {
	name string
}

func (p *stubProvider) Chat(ctx context.Context, req ChatRequest) (ChatResult, error) {
	return ChatResult{Content: "from " + p.name}, nil
}

func (p *stubProvider) ChatStream(ctx context.Context, req ChatRequest, onDelta func(string)) (ChatResult, error) {
	onDelta("from " + p.name)
	return ChatResult{Content: "from " + p.name}, nil
}

func TestRouterNamedExplicitKind(t *testing.T) {
	r := &Router{
		Anthropic: &stubProvider{name: "anthropic"},
		OpenAI:    &stubProvider{name: "openai"},
		Ollama:    &stubProvider{name: "ollama"},
	}

	for _, kind := range []string{"anthropic", "openai", "ollama"} {
		p, err := r.Named(kind)
		if err != nil {
			t.Fatalf("Named(%q) error = %v", kind, err)
		}
		result, err := p.Chat(context.Background(), ChatRequest{})
		if err != nil {
			t.Fatalf("Chat() error = %v", err)
		}
		if result.Content != "from "+kind {
			t.Fatalf("Named(%q) routed to wrong provider: got %q", kind, result.Content)
		}
	}
}

func TestRouterNamedUnconfiguredProvider(t *testing.T) {
	r := &Router{} // nothing configured

	for _, kind := range []string{"anthropic", "openai", "ollama"} {
		_, err := r.Named(kind)
		if err == nil {
			t.Fatalf("Named(%q) on an empty Router: expected error, got nil", kind)
		}
		if !strings.Contains(strings.ToLower(err.Error()), kind) && kind != "ollama" {
			// Anthropic/OpenAI error messages name the provider explicitly;
			// Ollama's mentions "Ollama backend" — just confirm it's a
			// "not configured" style error either way.
			t.Fatalf("Named(%q) error %q doesn't mention the provider", kind, err.Error())
		}
	}
}

func TestRouterNamedUnknownKind(t *testing.T) {
	r := &Router{Anthropic: &stubProvider{name: "anthropic"}}
	if _, err := r.Named("does-not-exist"); err == nil {
		t.Fatal("Named() with an unknown kind: expected error, got nil")
	}
}

// TestRouterChatWithProviderIgnoresModelPrefix is the crux of "switching
// Chat Provider changes the backend used": ChatWithProvider must dispatch
// purely on the explicit kind argument, never on req.Model's prefix (that
// guessing behavior — ProviderKind/providerFor — is reserved for direct
// POST /api/llm/chat callers who never state a provider). A model name
// that would normally sniff as "ollama" (no recognized prefix) must still
// reach Anthropic when the caller explicitly asked for "anthropic".
func TestRouterChatWithProviderIgnoresModelPrefix(t *testing.T) {
	r := &Router{
		Anthropic: &stubProvider{name: "anthropic"},
		Ollama:    &stubProvider{name: "ollama"},
	}

	result, err := r.ChatWithProvider(context.Background(), "anthropic", ChatRequest{Model: "some-locally-fine-tuned-model"})
	if err != nil {
		t.Fatalf("ChatWithProvider() error = %v", err)
	}
	if result.Content != "from anthropic" {
		t.Fatalf("ChatWithProvider(\"anthropic\", ...) with a non-claude-prefixed model name still routed elsewhere: got %q", result.Content)
	}

	// Sanity check: the same model name via the guessing path (ProviderKind)
	// really would have gone to Ollama, proving the two paths are distinct.
	if got := ProviderKind("some-locally-fine-tuned-model"); got != "ollama" {
		t.Fatalf("ProviderKind(...) = %q, want %q (test assumption broken)", got, "ollama")
	}
}

func TestRouterChatStreamWithProvider(t *testing.T) {
	r := &Router{Ollama: &stubProvider{name: "ollama"}}
	var deltas []string
	result, err := r.ChatStreamWithProvider(context.Background(), "ollama", ChatRequest{}, func(d string) { deltas = append(deltas, d) })
	if err != nil {
		t.Fatalf("ChatStreamWithProvider() error = %v", err)
	}
	if result.Content != "from ollama" || len(deltas) != 1 || deltas[0] != "from ollama" {
		t.Fatalf("unexpected stream result: content=%q deltas=%v", result.Content, deltas)
	}
}
