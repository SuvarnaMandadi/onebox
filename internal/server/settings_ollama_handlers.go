package server

import (
	"net/http"

	"onebox/internal/llm"
)

type ollamaModelsResponse struct {
	Version string   `json:"version"`
	Models  []string `json:"models"`
	// EmbeddingModelsExcluded counts how many of the daemon's installed
	// models were left out of Models because they look embedding-only
	// (see llm.IsEmbeddingModel) — e.g. nomic-embed-text. Surfaced so the
	// dashboard can tell an operator *why* a model they know they pulled
	// isn't in the dropdown, instead of it just silently not being there.
	EmbeddingModelsExcluded int `json:"embedding_models_excluded"`
}

// handleOllamaModels is admin-only: GET /api/settings/ollama-models?base_url=...
// backs the Chat Provider panel's "Ollama" model dropdown — it calls the
// named (or currently-saved) Ollama daemon's own /api/tags and /api/version
// endpoints and reports back what's actually installed and chat-capable,
// so the dashboard never has to guess model names, and never offers an
// embedding-only model (like nomic-embed-text) as a chat choice — that
// model belongs exclusively to the Embedding Provider panel. Version is
// best-effort: an older Ollama build without /api/version still returns
// its model list, just with an empty version string, rather than failing
// the whole request.
func (s *Server) handleOllamaModels(w http.ResponseWriter, r *http.Request) {
	baseURL := r.URL.Query().Get("base_url")
	if baseURL == "" {
		stored, err := getAllSettings(r.Context(), s.db, s.cfg.JWTSecret)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to load settings", nil)
			return
		}
		if v, ok := stored[settingOllamaBaseURL]; ok && v != "" {
			baseURL = v
		} else {
			baseURL = s.cfg.OllamaBaseURL
		}
	}

	client := llm.NewOllamaClient(baseURL)
	client.Client = httpTestClient // same 8s timeout used elsewhere in Settings
	all, err := client.ListModels(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "ollama_unreachable", "couldn't reach Ollama at "+baseURL+": "+err.Error(), nil)
		return
	}
	version, _ := client.Version(r.Context()) // best-effort — see doc comment

	chatModels := make([]string, 0, len(all))
	for _, m := range all {
		if !llm.IsEmbeddingModel(m) {
			chatModels = append(chatModels, m)
		}
	}

	writeJSON(w, http.StatusOK, ollamaModelsResponse{
		Version:                 version,
		Models:                  chatModels,
		EmbeddingModelsExcluded: len(all) - len(chatModels),
	})
}
