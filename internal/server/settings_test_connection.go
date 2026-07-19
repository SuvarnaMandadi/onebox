package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type testConnectionRequest struct {
	// Kind selects which provider to test: "anthropic", "openai", "ollama",
	// or "embedding" (tests whichever embedding provider — openai or
	// ollama — is named in EmbeddingProvider below, or the currently saved
	// one if that's left blank).
	Kind              string `json:"kind"`
	APIKey            string `json:"api_key"`
	BaseURL           string `json:"base_url"`
	EmbeddingProvider string `json:"embedding_provider"`
}

type testConnectionResponse struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

var httpTestClient = &http.Client{Timeout: 8 * time.Second}

// handleTestConnection is admin-only. It makes one minimal, real
// (non-generating, so no token cost against a paid API) call to the
// named provider — using the request's override fields if given, falling
// back to whatever's currently saved — and reports success or a
// human-readable failure reason immediately, so a self-hoster can verify
// their setup before saving it.
func (s *Server) handleTestConnection(w http.ResponseWriter, r *http.Request) {
	var req testConnectionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "request body must be valid JSON", nil)
		return
	}

	stored, err := getAllSettings(r.Context(), s.db, s.cfg.JWTSecret)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to load settings", nil)
		return
	}
	fallback := func(key settingKey, override, def string) string {
		if override != "" {
			return override
		}
		if v, ok := stored[key]; ok && v != "" {
			return v
		}
		return def
	}

	var result testConnectionResponse
	switch req.Kind {
	case "anthropic":
		apiKey := fallback(settingAnthropicAPIKey, req.APIKey, s.cfg.AnthropicAPIKey)
		result = testAnthropicConnection(r.Context(), apiKey)
	case "openai":
		apiKey := fallback(settingOpenAIAPIKey, req.APIKey, s.cfg.OpenAIChatAPIKey)
		baseURL := fallback(settingOpenAIBaseURL, req.BaseURL, "https://api.openai.com/v1")
		result = testOpenAICompatConnection(r.Context(), "OpenAI", baseURL, apiKey)
	case "ollama":
		baseURL := fallback(settingOllamaBaseURL, req.BaseURL, "http://localhost:11434")
		result = testOllamaConnection(r.Context(), baseURL)
	case "embedding":
		embeddingProvider := fallback(settingEmbeddingProvider, req.EmbeddingProvider, "openai")
		switch embeddingProvider {
		case "ollama":
			baseURL := fallback(settingOllamaBaseURL, req.BaseURL, "http://localhost:11434")
			result = testOllamaConnection(r.Context(), baseURL)
		case "voyage":
			apiKey := fallback(settingEmbeddingAPIKey, req.APIKey, "")
			baseURL := fallback(settingEmbeddingBaseURL, req.BaseURL, "https://api.voyageai.com/v1")
			result = testOpenAICompatConnection(r.Context(), "Voyage AI", baseURL, apiKey)
		default:
			apiKey := fallback(settingEmbeddingAPIKey, req.APIKey, "")
			baseURL := fallback(settingEmbeddingBaseURL, req.BaseURL, "https://api.openai.com/v1")
			result = testOpenAICompatConnection(r.Context(), "Embedding provider", baseURL, apiKey)
		}
	default:
		writeError(w, http.StatusBadRequest, "invalid_body", `kind must be one of "anthropic", "openai", "ollama", "embedding"`, nil)
		return
	}

	writeJSON(w, http.StatusOK, result)
}

func testAnthropicConnection(ctx context.Context, apiKey string) testConnectionResponse {
	if apiKey == "" {
		return testConnectionResponse{OK: false, Message: "No Anthropic API key set."}
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.anthropic.com/v1/models", nil)
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	return doTestRequest(req, "Anthropic", "https://api.anthropic.com")
}

func testOpenAICompatConnection(ctx context.Context, label, baseURL, apiKey string) testConnectionResponse {
	if apiKey == "" {
		return testConnectionResponse{OK: false, Message: fmt.Sprintf("No %s API key set.", label)}
	}
	url := strings.TrimRight(baseURL, "/") + "/models"
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+apiKey)
	return doTestRequest(req, label, baseURL)
}

func testOllamaConnection(ctx context.Context, baseURL string) testConnectionResponse {
	url := strings.TrimRight(baseURL, "/") + "/api/tags"
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	return doTestRequest(req, "Ollama", baseURL)
}

func doTestRequest(req *http.Request, label, baseURL string) testConnectionResponse {
	res, err := httpTestClient.Do(req)
	if err != nil {
		return testConnectionResponse{OK: false, Message: humanizeProviderError(err, label, baseURL)}
	}
	defer res.Body.Close()

	if res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden {
		return testConnectionResponse{OK: false, Message: fmt.Sprintf("%s rejected the API key — check it in Settings → Providers", label)}
	}
	if res.StatusCode == http.StatusNotFound {
		return testConnectionResponse{OK: false, Message: fmt.Sprintf("%s's URL looks wrong (%s) — check it in Settings → Providers", label, baseURL)}
	}
	if res.StatusCode >= 300 {
		return testConnectionResponse{OK: false, Message: fmt.Sprintf("%s responded with an unexpected error (HTTP %d)", label, res.StatusCode)}
	}
	return testConnectionResponse{OK: true, Message: fmt.Sprintf("%s connected successfully.", label)}
}
