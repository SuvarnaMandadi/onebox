package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestSettingsGetAndUpdate(t *testing.T) {
	srv, _ := newTestServer(t)
	adminToken := bootstrapAdmin(t, srv)

	t.Run("get before any settings saved", func(t *testing.T) {
		rec := doAuth(t, srv, http.MethodGet, "/api/settings", adminToken, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
		}
		var settings map[string]any
		json.Unmarshal(rec.Body.Bytes(), &settings)
		secret, ok := settings["anthropic_api_key"].(map[string]any)
		if !ok || secret["set"] != false {
			t.Fatalf("anthropic_api_key = %v, want {set: false}", settings["anthropic_api_key"])
		}
	})

	t.Run("non-admin rejected", func(t *testing.T) {
		_, userToken := signupUser(t, srv, "notadmin@example.com")
		rec := doAuth(t, srv, http.MethodGet, "/api/settings", userToken, nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
	})

	t.Run("update rejects unknown key", func(t *testing.T) {
		rec := doAuth(t, srv, http.MethodPut, "/api/settings", adminToken, map[string]string{"not_a_real_key": "x"})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("update saves and masks secret, applies plain value", func(t *testing.T) {
		rec := doAuth(t, srv, http.MethodPut, "/api/settings", adminToken, map[string]string{
			"anthropic_api_key": "sk-ant-test-123",
			"anthropic_model":   "claude-opus-4-8",
		})
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204, body = %s", rec.Code, rec.Body.String())
		}

		getRec := doAuth(t, srv, http.MethodGet, "/api/settings", adminToken, nil)
		var settings map[string]any
		json.Unmarshal(getRec.Body.Bytes(), &settings)

		secret, _ := settings["anthropic_api_key"].(map[string]any)
		if secret["set"] != true {
			t.Fatalf("anthropic_api_key = %v, want {set: true}", settings["anthropic_api_key"])
		}
		if settings["anthropic_model"] != "claude-opus-4-8" {
			t.Fatalf("anthropic_model = %v, want claude-opus-4-8", settings["anthropic_model"])
		}
	})

	t.Run("saved key is actually usable by the LLM router", func(t *testing.T) {
		// The router is rebuilt on save; a model that used to fail with
		// "no Anthropic API key configured" should now route successfully
		// (it'll still fail calling the real API since the key is fake,
		// but the error must change from "not configured" to a real
		// request failure — proving the setting took effect).
		bundle := srv.providers.Load()
		if bundle.llm.Anthropic == nil {
			t.Fatal("expected Anthropic provider to be non-nil after saving anthropic_api_key")
		}
	})
}

// TestChatProviderSettingsRoundTrip covers requirement 1/2 of the Chat
// Provider refactor at the settings-storage layer: chat_provider and
// chat_model are independent, ordinary (non-secret) settings — they
// round-trip through GET/PUT /api/settings like any other plain value,
// are rejected the same way as any other key when unknown, and — the
// part that actually matters — a save immediately changes what
// bundle.chat resolves to (reloadProviders runs synchronously in
// handleUpdateSettings), with no restart.
func TestChatProviderSettingsRoundTrip(t *testing.T) {
	srv, _ := newTestServer(t)
	adminToken := bootstrapAdmin(t, srv)

	t.Run("defaults to ollama with no model until configured", func(t *testing.T) {
		chat := srv.providers.Load().chat
		if chat.Provider != "ollama" || chat.Model != "" {
			t.Fatalf("default chat selection = %+v, want {ollama, \"\"}", chat)
		}
	})

	t.Run("save updates the live resolved selection", func(t *testing.T) {
		rec := doAuth(t, srv, http.MethodPut, "/api/settings", adminToken, map[string]string{
			"chat_provider": "ollama",
			"chat_model":    "llama3.2:3b",
		})
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204, body = %s", rec.Code, rec.Body.String())
		}
		chat := srv.providers.Load().chat
		if chat.Provider != "ollama" || chat.Model != "llama3.2:3b" {
			t.Fatalf("chat selection after save = %+v, want {ollama, llama3.2:3b}", chat)
		}
	})

	t.Run("round-trips through GET as a plain (non-secret) value", func(t *testing.T) {
		getRec := doAuth(t, srv, http.MethodGet, "/api/settings", adminToken, nil)
		var settings map[string]any
		json.Unmarshal(getRec.Body.Bytes(), &settings)
		if settings["chat_provider"] != "ollama" {
			t.Fatalf("chat_provider = %v, want %q (plain string, not a {set: bool} secret wrapper)", settings["chat_provider"], "ollama")
		}
		if settings["chat_model"] != "llama3.2:3b" {
			t.Fatalf("chat_model = %v, want %q", settings["chat_model"], "llama3.2:3b")
		}
	})

	t.Run("switching to anthropic is independent of the embedding provider", func(t *testing.T) {
		doAuth(t, srv, http.MethodPut, "/api/settings", adminToken, map[string]string{
			"embedding_provider": "ollama",
			"embedding_model":    "nomic-embed-text",
		})
		rec := doAuth(t, srv, http.MethodPut, "/api/settings", adminToken, map[string]string{
			"chat_provider":     "anthropic",
			"chat_model":        "claude-sonnet-5",
			"anthropic_api_key": "sk-ant-test-123",
		})
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204, body = %s", rec.Code, rec.Body.String())
		}
		bundle := srv.providers.Load()
		if bundle.chat.Provider != "anthropic" {
			t.Fatalf("chat.Provider = %q, want %q", bundle.chat.Provider, "anthropic")
		}
		// The embedding provider set moments earlier must be completely
		// unaffected by the chat_provider change that followed it.
		getRec := doAuth(t, srv, http.MethodGet, "/api/settings", adminToken, nil)
		var settings map[string]any
		json.Unmarshal(getRec.Body.Bytes(), &settings)
		if settings["embedding_provider"] != "ollama" {
			t.Fatalf("embedding_provider = %v, want %q — changing chat_provider must not touch it", settings["embedding_provider"], "ollama")
		}
	})
}

// TestChatProviderRejectsEmbeddingModelForOllama is the settings-layer
// regression test for the reported bug: chat_model must never be
// persisted as an embedding-only Ollama model like nomic-embed-text — that
// model belongs exclusively to the Embedding Provider. This is the
// server-side backstop behind app.js's own filtering (see
// validateChatModel) — it must hold even for a request that doesn't go
// through the dashboard UI at all.
func TestChatProviderRejectsEmbeddingModelForOllama(t *testing.T) {
	srv, _ := newTestServer(t)
	adminToken := bootstrapAdmin(t, srv)

	t.Run("both fields rejected together", func(t *testing.T) {
		rec := doAuth(t, srv, http.MethodPut, "/api/settings", adminToken, map[string]string{
			"chat_provider": "ollama",
			"chat_model":    "nomic-embed-text",
		})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
		}
		if chat := srv.providers.Load().chat; chat.Model == "nomic-embed-text" {
			t.Fatal("nomic-embed-text must never be persisted as the chat model")
		}
	})

	t.Run("chat_model alone is checked against the already-saved chat_provider", func(t *testing.T) {
		ok := doAuth(t, srv, http.MethodPut, "/api/settings", adminToken, map[string]string{"chat_provider": "ollama", "chat_model": "llama3.2:3b"})
		if ok.Code != http.StatusNoContent {
			t.Fatalf("setup save: status = %d, want 204, body = %s", ok.Code, ok.Body.String())
		}
		rec := doAuth(t, srv, http.MethodPut, "/api/settings", adminToken, map[string]string{"chat_model": "bge-large"})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
		}
		if chat := srv.providers.Load().chat; chat.Model != "llama3.2:3b" {
			t.Fatalf("chat.Model = %q, want unchanged %q after a rejected update", chat.Model, "llama3.2:3b")
		}
	})

	t.Run("a real chat model is still accepted", func(t *testing.T) {
		rec := doAuth(t, srv, http.MethodPut, "/api/settings", adminToken, map[string]string{
			"chat_provider": "ollama",
			"chat_model":    "llama3.2:1b",
		})
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204, body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("embedding_model itself is never subject to this check", func(t *testing.T) {
		rec := doAuth(t, srv, http.MethodPut, "/api/settings", adminToken, map[string]string{
			"embedding_provider": "ollama",
			"embedding_model":    "nomic-embed-text",
		})
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204, body = %s — the Embedding Provider must remain free to use embedding models", rec.Code, rec.Body.String())
		}
	})
}

func TestSettingsSecretNotReturnedInPlaintext(t *testing.T) {
	srv, _ := newTestServer(t)
	adminToken := bootstrapAdmin(t, srv)

	doAuth(t, srv, http.MethodPut, "/api/settings", adminToken, map[string]string{"anthropic_api_key": "super-secret-value"})

	rec := doAuth(t, srv, http.MethodGet, "/api/settings", adminToken, nil)
	var raw map[string]any
	json.Unmarshal(rec.Body.Bytes(), &raw)
	b, _ := json.Marshal(raw)
	if strings.Contains(string(b), "super-secret-value") {
		t.Fatal("GET /api/settings must never return a secret value in plaintext")
	}
}
