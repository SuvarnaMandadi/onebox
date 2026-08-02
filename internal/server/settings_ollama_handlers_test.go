package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestOllamaModelsEndpoint backs the Chat Provider panel's "automatically
// load installed models" requirement: GET /api/settings/ollama-models must
// proxy a real Ollama daemon's /api/tags and /api/version, admin-gated,
// and fail clearly (not silently return an empty list) when unreachable.
func TestOllamaModelsEndpoint(t *testing.T) {
	fakeOllama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			json.NewEncoder(w).Encode(map[string]any{
				"models": []map[string]string{{"name": "llama3.2:3b"}, {"name": "qwen"}, {"name": "nomic-embed-text"}},
			})
		case "/api/version":
			json.NewEncoder(w).Encode(map[string]string{"version": "0.5.4"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer fakeOllama.Close()

	srv, _ := newTestServer(t)
	adminToken := bootstrapAdmin(t, srv)

	t.Run("non-admin rejected", func(t *testing.T) {
		_, userToken := signupUser(t, srv, "notadmin@example.com")
		rec := doAuth(t, srv, http.MethodGet, "/api/settings/ollama-models?base_url="+fakeOllama.URL, userToken, nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
	})

	t.Run("returns installed chat models and version, excluding embedding models", func(t *testing.T) {
		rec := doAuth(t, srv, http.MethodGet, "/api/settings/ollama-models?base_url="+fakeOllama.URL, adminToken, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
		}
		var resp ollamaModelsResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if resp.Version != "0.5.4" {
			t.Fatalf("version = %q, want %q", resp.Version, "0.5.4")
		}
		// The fake daemon also has nomic-embed-text installed — it must
		// never appear in Models, since that's exactly the reported bug
		// (an embedding model showing up as a chat model choice).
		if len(resp.Models) != 2 || resp.Models[0] != "llama3.2:3b" || resp.Models[1] != "qwen" {
			t.Fatalf("models = %v, want [llama3.2:3b qwen] (nomic-embed-text must be filtered out)", resp.Models)
		}
		if resp.EmbeddingModelsExcluded != 1 {
			t.Fatalf("embedding_models_excluded = %d, want 1", resp.EmbeddingModelsExcluded)
		}
	})

	t.Run("unreachable daemon fails clearly, not silently empty", func(t *testing.T) {
		rec := doAuth(t, srv, http.MethodGet, "/api/settings/ollama-models?base_url=http://127.0.0.1:1", adminToken, nil)
		if rec.Code != http.StatusBadGateway {
			t.Fatalf("status = %d, want 502, body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("daemon with only an embedding model installed returns an empty chat model list, not an error", func(t *testing.T) {
		embedOnly := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/tags":
				json.NewEncoder(w).Encode(map[string]any{
					"models": []map[string]string{{"name": "nomic-embed-text"}},
				})
			case "/api/version":
				json.NewEncoder(w).Encode(map[string]string{"version": "0.5.4"})
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer embedOnly.Close()

		rec := doAuth(t, srv, http.MethodGet, "/api/settings/ollama-models?base_url="+embedOnly.URL, adminToken, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
		}
		var resp ollamaModelsResponse
		json.Unmarshal(rec.Body.Bytes(), &resp)
		if len(resp.Models) != 0 {
			t.Fatalf("models = %v, want empty", resp.Models)
		}
		if resp.EmbeddingModelsExcluded != 1 {
			t.Fatalf("embedding_models_excluded = %d, want 1", resp.EmbeddingModelsExcluded)
		}
	})
}
