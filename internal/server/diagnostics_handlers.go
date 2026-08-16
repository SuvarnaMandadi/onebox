// diagnostics_handlers.go is the HTTP surface for the whole
// provider-diagnostics subsystem — Section 11's architecture requirement
// ("move provider checks into backend services; frontend never performs
// provider logic directly") lives here: every handler below does real
// work in Go and returns a finished, structured result; the React
// Settings page (web/src/pages/settings-page.tsx) only ever renders what
// these endpoints return, never talks to Ollama/Anthropic/OpenAI itself.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"onebox/internal/llm"
)

type diagnosticsRequest struct {
	// Provider selects which diagnoser to run: "ollama", "anthropic",
	// "openai", or "embedding" (diagnoses whichever embedding provider —
	// ollama or an OpenAI-compatible one — is currently configured,
	// mirroring testConnectionRequest.Kind="embedding" in
	// settings_test_connection.go).
	Provider       string `json:"provider"`
	BaseURL        string `json:"base_url,omitempty"`
	APIKey         string `json:"api_key,omitempty"`
	Model          string `json:"model,omitempty"`
	EmbeddingModel string `json:"embedding_model,omitempty"`
	// Deep gates the real generate/chat/streaming calls (see
	// ollamaDiagnosticsParams.DeepCheck) — false by default so opening
	// the Settings page doesn't silently start burning tokens against a
	// paid account; the panel's "Run full diagnostics" action sets it.
	Deep bool `json:"deep,omitempty"`
}

// handleProviderDiagnostics is admin-only: POST /api/settings/diagnostics.
// Runs the real, live checklist for one provider (Sections 1-3 and 6),
// records the outcome to Connection History (Section 7), and returns the
// full report. Request fields override whatever's currently saved,
// exactly like handleTestConnection's fallback pattern — this lets the
// Settings form diagnose what the operator is about to save, before they
// save it (Section 8).
func (s *Server) handleProviderDiagnostics(w http.ResponseWriter, r *http.Request) {
	var req diagnosticsRequest
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

	var report providerDiagnosticsReport
	switch req.Provider {
	case "ollama":
		report = diagnoseOllama(r.Context(), ollamaDiagnosticsParams{
			BaseURL:        fallback(settingOllamaBaseURL, req.BaseURL, s.cfg.OllamaBaseURL),
			SelectedModel:  fallback(settingChatModel, req.Model, s.cfg.ChatModel),
			EmbeddingModel: req.EmbeddingModel,
			DeepCheck:      req.Deep,
		})
	case "anthropic":
		report = diagnoseHostedProvider(r.Context(), hostedDiagnosticsParams{
			Kind:          hostedAnthropic,
			APIKey:        fallback(settingAnthropicAPIKey, req.APIKey, s.cfg.AnthropicAPIKey),
			SelectedModel: fallback(settingAnthropicModel, req.Model, s.cfg.AnthropicModel),
			DeepCheck:     req.Deep,
		})
	case "openai":
		report = diagnoseHostedProvider(r.Context(), hostedDiagnosticsParams{
			Kind:          hostedOpenAI,
			APIKey:        fallback(settingOpenAIAPIKey, req.APIKey, s.cfg.OpenAIChatAPIKey),
			BaseURL:       fallback(settingOpenAIBaseURL, req.BaseURL, "https://api.openai.com/v1"),
			SelectedModel: fallback(settingChatModel, req.Model, ""),
			DeepCheck:     req.Deep,
		})
	case "embedding":
		embeddingProvider := fallback(settingEmbeddingProvider, "", "openai")
		embeddingModel := fallback(settingEmbeddingModel, req.EmbeddingModel, "")
		if embeddingProvider == "ollama" {
			baseURL := fallback(settingOllamaBaseURL, req.BaseURL, s.cfg.OllamaBaseURL)
			// DeepCheck is deliberately NOT passed through here: Ollama's
			// deep check (runOllamaDeepChecks) makes a /api/generate and
			// /api/chat call, both meaningless — and liable to just fail —
			// against an embedding-only model. An embedding model has no
			// SelectedModel set on this params struct either way, so
			// runOllamaDeepChecks would already no-op (see its
			// report.SelectedModelFound gate), but the real embeddings
			// probe below is the correct deep check for this path.
			report = diagnoseOllama(r.Context(), ollamaDiagnosticsParams{
				BaseURL:        baseURL,
				EmbeddingModel: embeddingModel,
			})
			if req.Deep && report.EmbeddingModelFound {
				runOllamaEmbeddingDeepCheck(r.Context(), baseURL, embeddingModel, &report)
			}
		} else {
			baseURL := fallback(settingEmbeddingBaseURL, req.BaseURL, "https://api.openai.com/v1")
			label := "OpenAI"
			if embeddingProvider == "voyage" {
				baseURL = fallback(settingEmbeddingBaseURL, req.BaseURL, "https://api.voyageai.com/v1")
				label = "Voyage AI"
			}
			apiKey := fallback(settingEmbeddingAPIKey, req.APIKey, "")
			// Voyage (and, from this codebase's point of view, any other
			// OpenAI-compatible *embeddings-only* provider) has no chat
			// completions endpoint at all, and neither is documented to
			// expose a lightweight GET /models list the way OpenAI does —
			// so unlike diagnoseHostedProvider (chat providers), the only
			// honest way to verify reachability/auth/model here is a real
			// (deliberately deep-gated) embeddings call. See
			// diagnoseHostedEmbeddingProvider's own doc comment.
			report = diagnoseHostedEmbeddingProvider(r.Context(), label, baseURL, apiKey, embeddingModel, req.Deep)
		}
	default:
		writeError(w, http.StatusBadRequest, "invalid_body", `provider must be one of "ollama", "anthropic", "openai", "embedding"`, nil)
		return
	}

	if err := recordDiagnosticsRun(r.Context(), s.db, report); err != nil {
		// Connection History is a nice-to-have, not load-bearing — a
		// failure to write it must never hide the diagnostic result the
		// admin actually asked for.
		fmt.Printf("record diagnostics run: %v\n", err)
	}

	writeJSON(w, http.StatusOK, report)
}

// handleDiagnosticsHistory is admin-only: GET
// /api/settings/diagnostics/history?provider=&limit= — Section 7.
func (s *Server) handleDiagnosticsHistory(w http.ResponseWriter, r *http.Request) {
	provider := r.URL.Query().Get("provider")
	limit := diagnosticsHistoryLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			limit = n
		}
	}
	entries, err := listDiagnosticsHistory(r.Context(), s.db, provider, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to load diagnostics history", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": entries})
}

// handleProviderPerformance is admin-only: GET
// /api/settings/performance?provider=... — Section 5.
func (s *Server) handleProviderPerformance(w http.ResponseWriter, r *http.Request) {
	provider := r.URL.Query().Get("provider")
	if provider == "" {
		writeError(w, http.StatusBadRequest, "invalid_query", "provider query parameter is required", nil)
		return
	}
	summary, err := computePerformanceSummary(r.Context(), s.db, provider)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to compute performance summary", nil)
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

type ollamaPullRequestBody struct {
	BaseURL string `json:"base_url,omitempty"`
	Model   string `json:"model"`
}

// ollamaPullClient has its own long timeout budget — pulling a real
// multi-gigabyte model can legitimately take minutes, unlike every other
// diagnostic call in this file, which uses the shared 8s httpTestClient.
var ollamaPullClient = &http.Client{Timeout: 30 * time.Minute}

// handleOllamaPull is admin-only: POST /api/settings/ollama-pull — Section
// 9's "Pull model" one-click fix. Streams the daemon's own real download
// progress back as SSE (never a synthesized progress bar) using the same
// event-stream convention the chat/LLM streaming endpoints already use
// (see llm_handlers.go's streamLLMChat).
func (s *Server) handleOllamaPull(w http.ResponseWriter, r *http.Request) {
	var req ollamaPullRequestBody
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Model == "" {
		writeError(w, http.StatusBadRequest, "invalid_body", `request body must be JSON with a non-empty "model"`, nil)
		return
	}

	baseURL := req.BaseURL
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

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming_unsupported", "server does not support streaming", nil)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	// Pulling must survive the request's own deadline being shorter than
	// the download — use context.Background with the client's own
	// generous timeout instead of r.Context(), but still stop promptly
	// if the browser disconnects (r.Context().Done()).
	ctx, cancel := context.WithTimeout(context.Background(), ollamaPullClient.Timeout)
	defer cancel()
	go func() {
		<-r.Context().Done()
		cancel()
	}()

	client := llm.NewOllamaClient(baseURL)
	client.Client = ollamaPullClient

	pullErr := client.Pull(ctx, req.Model, func(p llm.OllamaPullProgress) {
		data, _ := json.Marshal(p)
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	})
	if pullErr != nil {
		data, _ := json.Marshal(map[string]string{"error": pullErr.Error()})
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
		return
	}
	fmt.Fprintf(w, "data: %s\n\n", `{"done":true}`)
	flusher.Flush()
}

// -- Section 8: real-time configuration validation --------------------------

type configIssue struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// handleValidateSettings is admin-only: POST /api/settings/validate —
// checks a candidate settings body for problems a human would otherwise
// only discover after clicking Save (bad URL syntax, a chat
// provider/model combination that can't work together, an obviously
// missing required field) — Section 8's "do NOT wait until the user
// clicks Save." Deliberately does not make any network calls itself
// (that's what /api/settings/diagnostics is for) — this is fast,
// synchronous, structural validation only, safe to call on every
// keystroke debounce.
func (s *Server) handleValidateSettings(w http.ResponseWriter, r *http.Request) {
	var body map[string]string
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "request body must be a JSON object of key: value", nil)
		return
	}

	stored, err := getAllSettings(r.Context(), s.db, s.cfg.JWTSecret)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to load settings", nil)
		return
	}
	merged := map[settingKey]string{}
	for k, v := range stored {
		merged[k] = v
	}
	for k, v := range body {
		merged[settingKey(k)] = v
	}

	issues := validateSettingsStructural(merged)
	writeJSON(w, http.StatusOK, map[string]any{"issues": issues, "valid": len(issues) == 0})
}

func validateSettingsStructural(merged map[settingKey]string) []configIssue {
	var issues []configIssue
	checkURL := func(key settingKey, label string) {
		v := merged[key]
		if v == "" {
			return
		}
		if !isValidHTTPURL(v) {
			issues = append(issues, configIssue{Field: string(key), Message: fmt.Sprintf("%s doesn't look like a valid http(s) URL: %q", label, v)})
		}
	}
	checkURL(settingOllamaBaseURL, "Ollama base URL")
	checkURL(settingOpenAIBaseURL, "OpenAI base URL")
	checkURL(settingEmbeddingBaseURL, "Embedding base URL")

	chatProvider := merged[settingChatProvider]
	chatModel := merged[settingChatModel]
	switch chatProvider {
	case "anthropic":
		if merged[settingAnthropicAPIKey] == "" {
			issues = append(issues, configIssue{Field: string(settingChatProvider), Message: "Chat provider is Anthropic, but no Anthropic API key is set"})
		}
	case "openai":
		if merged[settingOpenAIAPIKey] == "" {
			issues = append(issues, configIssue{Field: string(settingChatProvider), Message: "Chat provider is OpenAI, but no OpenAI API key is set"})
		}
	case "ollama":
		if chatModel != "" && llm.IsEmbeddingModel(chatModel) {
			issues = append(issues, configIssue{Field: string(settingChatModel), Message: fmt.Sprintf("%q looks like an embedding-only model, not a chat model", chatModel)})
		}
	case "":
		// no chat provider chosen yet — not an error, just unconfigured
	default:
		issues = append(issues, configIssue{Field: string(settingChatProvider), Message: fmt.Sprintf("unrecognized chat provider %q", chatProvider)})
	}

	embeddingProvider := merged[settingEmbeddingProvider]
	if embeddingProvider != "" && embeddingProvider != "openai" && embeddingProvider != "ollama" && embeddingProvider != "voyage" {
		issues = append(issues, configIssue{Field: string(settingEmbeddingProvider), Message: fmt.Sprintf("unrecognized embedding provider %q", embeddingProvider)})
	}
	if embeddingProvider == "openai" || embeddingProvider == "voyage" {
		if merged[settingEmbeddingAPIKey] == "" {
			issues = append(issues, configIssue{Field: string(settingEmbeddingAPIKey), Message: fmt.Sprintf("Embedding provider is %s, but no API key is set", embeddingProvider)})
		}
	}
	if embeddingProvider == "openai" || embeddingProvider == "voyage" {
		if merged[settingEmbeddingModel] == "" {
			issues = append(issues, configIssue{Field: string(settingEmbeddingModel), Message: "No embedding model set"})
		}
	}

	return issues
}

func isValidHTTPURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return false
	}
	return u.Scheme == "http" || u.Scheme == "https"
}
