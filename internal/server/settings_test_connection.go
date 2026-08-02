package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"onebox/internal/llm"
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
	// Model, if set, is additionally checked against the provider: for
	// Ollama, that it's actually installed (present in GET /api/tags);
	// for Anthropic/OpenAI, that it's listed by the provider's own
	// GET /models. Used by the Chat Provider panel so "Test connection"
	// verifies the selected model, not just reachability — an unset Model
	// (as every pre-existing caller sends) skips this check entirely, so
	// old behavior for "anthropic"/"openai"/"ollama"/"embedding" is
	// unchanged.
	Model string `json:"model"`
}

type testConnectionResponse struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
	// Version and Models are populated only for kind="ollama" — the
	// daemon's own reported version and its installed model tags — so the
	// Chat Provider panel can show them without a second round trip.
	Version string   `json:"version,omitempty"`
	Models  []string `json:"models,omitempty"`
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
		result = testAnthropicConnection(r.Context(), apiKey, req.Model)
	case "openai":
		apiKey := fallback(settingOpenAIAPIKey, req.APIKey, s.cfg.OpenAIChatAPIKey)
		baseURL := fallback(settingOpenAIBaseURL, req.BaseURL, "https://api.openai.com/v1")
		result = testOpenAICompatConnection(r.Context(), "OpenAI", baseURL, apiKey, req.Model)
	case "ollama":
		baseURL := fallback(settingOllamaBaseURL, req.BaseURL, "http://localhost:11434")
		result = testOllamaConnection(r.Context(), baseURL, req.Model)
	case "embedding":
		embeddingProvider := fallback(settingEmbeddingProvider, req.EmbeddingProvider, "openai")
		switch embeddingProvider {
		case "ollama":
			baseURL := fallback(settingOllamaBaseURL, req.BaseURL, "http://localhost:11434")
			result = testOllamaConnection(r.Context(), baseURL, req.Model)
		case "voyage":
			apiKey := fallback(settingEmbeddingAPIKey, req.APIKey, "")
			baseURL := fallback(settingEmbeddingBaseURL, req.BaseURL, "https://api.voyageai.com/v1")
			result = testOpenAICompatConnection(r.Context(), "Voyage AI", baseURL, apiKey, "")
		default:
			apiKey := fallback(settingEmbeddingAPIKey, req.APIKey, "")
			baseURL := fallback(settingEmbeddingBaseURL, req.BaseURL, "https://api.openai.com/v1")
			result = testOpenAICompatConnection(r.Context(), "Embedding provider", baseURL, apiKey, "")
		}
	default:
		writeError(w, http.StatusBadRequest, "invalid_body", `kind must be one of "anthropic", "openai", "ollama", "embedding"`, nil)
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// modelListedIn reports whether want is present in got — used to turn a
// successful-but-silent connection test into a real "is the model I
// picked actually available" check. An empty want always passes: callers
// that don't care about a specific model (every kind other than the Chat
// Provider panel) get identical behavior to before this field existed.
func modelListedIn(want string, got []string) bool {
	if want == "" {
		return true
	}
	for _, m := range got {
		if m == want {
			return true
		}
	}
	return false
}

func testAnthropicConnection(ctx context.Context, apiKey, model string) testConnectionResponse {
	if apiKey == "" {
		return testConnectionResponse{OK: false, Message: "No Anthropic API key set."}
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.anthropic.com/v1/models", nil)
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	result, ids := doTestRequestWithModelList(req, "Anthropic", "https://api.anthropic.com")
	if result.OK && !modelListedIn(model, ids) {
		return testConnectionResponse{OK: false, Message: fmt.Sprintf("Anthropic connected, but model %q wasn't found — check the model ID in Settings → Chat Provider", model)}
	}
	return result
}

func testOpenAICompatConnection(ctx context.Context, label, baseURL, apiKey, model string) testConnectionResponse {
	if apiKey == "" {
		return testConnectionResponse{OK: false, Message: fmt.Sprintf("No %s API key set.", label)}
	}
	url := strings.TrimRight(baseURL, "/") + "/models"
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+apiKey)
	result, ids := doTestRequestWithModelList(req, label, baseURL)
	if result.OK && !modelListedIn(model, ids) {
		return testConnectionResponse{OK: false, Message: fmt.Sprintf("%s connected, but model %q wasn't found in its /models list — check the model name in Settings", label, model)}
	}
	return result
}

func testOllamaConnection(ctx context.Context, baseURL, model string) testConnectionResponse {
	client := llm.NewOllamaClient(baseURL)
	client.Client = httpTestClient // same 8s test timeout every other check here uses
	models, err := client.ListModels(ctx)
	if err != nil {
		return testConnectionResponse{OK: false, Message: humanizeProviderError(err, "Ollama", baseURL)}
	}
	version, _ := client.Version(ctx) // best-effort — an older Ollama build may not expose it

	if !modelListedIn(model, models) {
		return testConnectionResponse{
			OK:      false,
			Message: fmt.Sprintf("Ollama connected, but %q isn't installed — run `ollama pull %s`, or pick one of the models found", model, model),
			Version: version,
			Models:  models,
		}
	}
	return testConnectionResponse{OK: true, Message: "Ollama connected successfully.", Version: version, Models: models}
}

// modelsListResponse matches the {"data": [{"id": "..."}]} shape shared by
// Anthropic's GET /v1/models and the OpenAI-compatible GET /models — good
// enough to extract available model IDs without needing a provider-specific
// parser for each.
type modelsListResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

// doTestRequestWithModelList does the same reachability/auth check as
// before, plus (on success) parses the response body for a model ID list —
// callers that don't care about a specific model just ignore the second
// return value, so this is a strict superset of the old doTestRequest.
func doTestRequestWithModelList(req *http.Request, label, baseURL string) (testConnectionResponse, []string) {
	res, err := httpTestClient.Do(req)
	if err != nil {
		return testConnectionResponse{OK: false, Message: humanizeProviderError(err, label, baseURL)}, nil
	}
	defer res.Body.Close()

	if res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden {
		return testConnectionResponse{OK: false, Message: fmt.Sprintf("%s rejected the API key — check it in Settings → Providers", label)}, nil
	}
	if res.StatusCode == http.StatusNotFound {
		return testConnectionResponse{OK: false, Message: fmt.Sprintf("%s's URL looks wrong (%s) — check it in Settings → Providers", label, baseURL)}, nil
	}
	if res.StatusCode >= 300 {
		return testConnectionResponse{OK: false, Message: fmt.Sprintf("%s responded with an unexpected error (HTTP %d)", label, res.StatusCode)}, nil
	}

	var parsed modelsListResponse
	var ids []string
	if json.NewDecoder(res.Body).Decode(&parsed) == nil {
		ids = make([]string, len(parsed.Data))
		for i, m := range parsed.Data {
			ids[i] = m.ID
		}
	}
	return testConnectionResponse{OK: true, Message: fmt.Sprintf("%s connected successfully.", label)}, ids
}
