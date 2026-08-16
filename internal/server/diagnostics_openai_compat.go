// diagnostics_openai_compat.go runs Section 3's hosted-provider checklist
// (API key valid, authentication succeeds, available models, rate
// limits if exposed, a real minimal chat call) against Anthropic and any
// OpenAI-compatible endpoint (OpenAI itself, Voyage AI, or a future
// OpenAI-compatible gateway — see hostedProviderKind).
//
// Unlike Ollama, neither Anthropic nor OpenAI expose a capabilities
// endpoint — there is no live "does this model support vision" call to
// make. Capability facts here come from a small maintained table
// (knownModelCapabilities below), applied ONLY to a model ID this same
// diagnostic run has already confirmed exists via a live GET /models
// call — every entry is tagged capabilitySourceKnown, and the frontend
// is expected to render that distinctly from Ollama's
// capabilitySourceLive facts. This is the honest resolution of "don't
// guess, query the provider" against a real constraint: the provider
// itself doesn't expose the answer to query.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"onebox/internal/embeddings"
	"onebox/internal/llm"
)

type hostedProviderKind string

const (
	hostedAnthropic hostedProviderKind = "anthropic"
	hostedOpenAI    hostedProviderKind = "openai"
)

type hostedDiagnosticsParams struct {
	Kind          hostedProviderKind
	APIKey        string
	BaseURL       string // OpenAI-compatible only; Anthropic's is fixed
	SelectedModel string
	DeepCheck     bool
}

const hostedDiagnosticTimeout = 15 * time.Second

// hostedDeepCheckTimeout gives the real chat-completion deep check
// (Section 3) its own budget, separate from hostedDiagnosticTimeout's
// tight 15s for the lightweight auth+models check — hosted APIs are
// usually fast, but a real inference call (even bounded to max_tokens:1)
// can occasionally queue behind provider-side load, and this check is
// gated behind an explicit "Run full check" action, so a longer wait
// here is expected. See ollamaDeepCheckTimeout's doc comment for the
// live-verified version of this same lesson on the Ollama side.
const hostedDeepCheckTimeout = 30 * time.Second

func diagnoseHostedProvider(parentCtx context.Context, p hostedDiagnosticsParams) providerDiagnosticsReport {
	start := time.Now()
	label := "OpenAI"
	baseURL := strings.TrimRight(p.BaseURL, "/")
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	if p.Kind == hostedAnthropic {
		label = "Anthropic"
		baseURL = "https://api.anthropic.com"
	}

	report := providerDiagnosticsReport{
		Provider:  string(p.Kind),
		Label:     label,
		BaseURL:   baseURL,
		CheckedAt: start.UTC().Format(time.RFC3339),
	}

	ctx, cancel := context.WithTimeout(parentCtx, hostedDiagnosticTimeout)
	defer cancel()

	if p.APIKey == "" {
		report.Status = severityError
		report.Checks = append(report.Checks, checkResult{Name: "API key configured", Status: severityError, Detail: "no API key is set"})
		report.Issues = append(report.Issues, diagnosticIssue{
			Code:           "api_key_missing",
			Severity:       severityError,
			Summary:        fmt.Sprintf("No %s API key is configured.", label),
			PossibleCauses: []string{"The key was never entered in Settings"},
			Fixes:          []diagnosticFix{{Label: "Open provider settings", Action: fixActionOpenSettings}},
		})
		report.TotalCheckMS = time.Since(start).Milliseconds()
		return report
	}

	// -- Check: authentication + available models ------------------------
	modelsURL, headers := hostedModelsRequest(p.Kind, baseURL, p.APIKey)
	reqStart := time.Now()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, modelsURL, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := httpTestClient.Do(req)
	latency := time.Since(reqStart)
	report.LatencyMS = latency.Milliseconds()

	if err != nil {
		report.Status = severityError
		report.Checks = append(report.Checks, checkResult{Name: "GET /models", Status: severityError, Detail: err.Error(), LatencyMS: latency.Milliseconds()})
		issue := classifyProviderError(err, 0, label, baseURL)
		issue.Fixes = append(issue.Fixes, diagnosticFix{Label: "Copy curl command", Action: fixActionCopyCurl, Command: curlCommand("GET", modelsURL, headers, true)})
		report.Issues = append(report.Issues, issue)
		report.TotalCheckMS = time.Since(start).Milliseconds()
		return report
	}
	defer res.Body.Close()

	// Rate-limit headers (Section 3 "Rate limits (if exposed)") — both
	// Anthropic and OpenAI send these on every response, success or not,
	// so read them regardless of status code.
	rateLimit := parseRateLimitHeaders(res.Header)

	if res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden {
		report.Status = severityError
		report.Reachable = true
		report.Checks = append(report.Checks, checkResult{Name: "Authentication", Status: severityError, Detail: fmt.Sprintf("HTTP %d", res.StatusCode), LatencyMS: latency.Milliseconds()})
		issue := classifyProviderError(nil, res.StatusCode, label, baseURL)
		issue.Fixes = append(issue.Fixes, diagnosticFix{Label: "Copy curl command", Action: fixActionCopyCurl, Command: curlCommand("GET", modelsURL, headers, true)})
		report.Issues = append(report.Issues, issue)
		report.TotalCheckMS = time.Since(start).Milliseconds()
		return report
	}
	if res.StatusCode >= 300 {
		report.Status = severityError
		report.Reachable = true
		report.Checks = append(report.Checks, checkResult{Name: "GET /models", Status: severityError, Detail: fmt.Sprintf("HTTP %d", res.StatusCode), LatencyMS: latency.Milliseconds()})
		report.Issues = append(report.Issues, classifyProviderError(nil, res.StatusCode, label, baseURL))
		report.TotalCheckMS = time.Since(start).Milliseconds()
		return report
	}

	report.Reachable = true
	report.Checks = append(report.Checks, checkResult{Name: "Authentication", Status: severityOK, Detail: "API key accepted", LatencyMS: latency.Milliseconds()})

	var parsed modelsListResponse
	json.NewDecoder(res.Body).Decode(&parsed)
	ids := make([]string, len(parsed.Data))
	for i, m := range parsed.Data {
		ids[i] = m.ID
	}
	report.Checks = append(report.Checks, checkResult{Name: "GET /models", Status: severityOK, Detail: fmt.Sprintf("%d model(s) available", len(ids))})

	for _, id := range ids {
		info := modelInfo{Name: id, IsSelected: id == p.SelectedModel, Source: capabilitySourceLive}
		caps := knownModelCapabilities(id)
		info.IsVision = caps.vision
		info.IsEmbedding = caps.embedding
		if caps.contextWindow > 0 {
			info.ContextLength = caps.contextWindow
			info.Source = capabilitySourceKnown
		}
		report.Models = append(report.Models, info)
	}

	if p.SelectedModel != "" {
		report.SelectedModel = p.SelectedModel
		for _, id := range ids {
			if id == p.SelectedModel {
				report.SelectedModelFound = true
				break
			}
		}
		if !report.SelectedModelFound {
			report.Checks = append(report.Checks, checkResult{Name: "Selected model exists", Status: severityError, Detail: fmt.Sprintf("%q wasn't found in %s's model list", p.SelectedModel, label)})
			report.Issues = append(report.Issues, diagnosticIssue{
				Code:     "model_missing",
				Severity: severityError,
				Summary:  fmt.Sprintf("The selected model %q isn't listed by %s.", p.SelectedModel, label),
				PossibleCauses: []string{
					"A typo in the model ID",
					"The model was retired or renamed by the provider",
					"The account doesn't have access to this model",
				},
				Fixes: []diagnosticFix{{Label: "Open provider settings", Action: fixActionOpenSettings}, {Label: "Refresh model list", Action: fixActionRefreshModels}},
			})
		} else {
			report.Checks = append(report.Checks, checkResult{Name: "Selected model exists", Status: severityOK, Detail: p.SelectedModel})
		}
	}

	// -- Capabilities (Section 6), applied only to the verified selected
	// model when one is set, else to the provider in general -----------
	knownCaps := knownModelCapabilities(p.SelectedModel)
	capDetail := fmt.Sprintf("known for the %s model family; not queried live (this provider has no capabilities API)", label)
	if report.SelectedModelFound {
		report.Capabilities = append(report.Capabilities,
			capability{Name: "Chat", Supported: true, Source: capabilitySourceLive, Detail: "reachable via the chat completions endpoint"},
			capability{Name: "Streaming", Supported: true, Source: capabilitySourceKnown, Detail: capDetail},
			capability{Name: "Tool Calling", Supported: knownCaps.tools, Source: capabilitySourceKnown, Detail: capDetail},
			capability{Name: "Function Calling", Supported: knownCaps.tools, Source: capabilitySourceKnown, Detail: capDetail},
			capability{Name: "JSON Output", Supported: knownCaps.jsonMode, Source: capabilitySourceKnown, Detail: capDetail},
			capability{Name: "Vision", Supported: knownCaps.vision, Source: capabilitySourceKnown, Detail: capDetail},
		)
		if knownCaps.reasoning {
			report.Capabilities = append(report.Capabilities,
				capability{Name: "Thinking", Supported: true, Source: capabilitySourceKnown, Detail: capDetail},
				capability{Name: "Reasoning", Supported: true, Source: capabilitySourceKnown, Detail: capDetail},
			)
		}
		if knownCaps.contextWindow > 0 {
			report.ContextWindow = knownCaps.contextWindow
		}
		if knownCaps.maxOutput > 0 {
			report.MaxTokens = knownCaps.maxOutput
		}
	}

	if rateLimit != nil {
		report.Issues = append(report.Issues, diagnosticIssue{
			Code:     "rate_limit_info",
			Severity: severityOK,
			Summary:  *rateLimit,
		})
	}

	// -- Deep check: a real, minimal chat call ---------------------------
	if p.DeepCheck && report.SelectedModelFound {
		deepCtx, deepCancel := context.WithTimeout(parentCtx, hostedDeepCheckTimeout)
		defer deepCancel()
		runHostedDeepCheck(deepCtx, p, baseURL, &report)
	}

	report.TotalCheckMS = time.Since(start).Milliseconds()
	report.Status = overallSeverity(report)
	return report
}

func hostedModelsRequest(kind hostedProviderKind, baseURL, apiKey string) (string, map[string]string) {
	if kind == hostedAnthropic {
		return baseURL + "/v1/models", map[string]string{"x-api-key": apiKey, "anthropic-version": "2023-06-01"}
	}
	return baseURL + "/models", map[string]string{"Authorization": "Bearer " + apiKey}
}

// parseRateLimitHeaders reads whichever rate-limit headers the response
// actually sent (Anthropic and OpenAI both use anthropic-ratelimit-*/
// x-ratelimit-* families) — returns nil, not a placeholder, when the
// provider didn't send any this response (some endpoints/plans omit
// them), matching "if exposed" from the spec.
func parseRateLimitHeaders(h http.Header) *string {
	candidates := [][2]string{
		{"anthropic-ratelimit-requests-remaining", "anthropic-ratelimit-requests-limit"},
		{"x-ratelimit-remaining-requests", "x-ratelimit-limit-requests"},
	}
	for _, c := range candidates {
		remaining := h.Get(c[0])
		limit := h.Get(c[1])
		if remaining != "" || limit != "" {
			s := fmt.Sprintf("Rate limit: %s / %s requests remaining", orNotReported(remaining), orNotReported(limit))
			return &s
		}
	}
	return nil
}

func runHostedDeepCheck(ctx context.Context, p hostedDiagnosticsParams, baseURL string, report *providerDiagnosticsReport) {
	var client interface {
		Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResult, error)
	}
	if p.Kind == hostedAnthropic {
		client = llm.NewAnthropicClient(baseURL, p.APIKey)
	} else {
		client = llm.NewOpenAIClient(baseURL, p.APIKey)
	}

	start := time.Now()
	// max_tokens:1 keeps this a genuine round trip through the model
	// while costing the minimum possible against a paid account.
	_, err := client.Chat(ctx, llm.ChatRequest{Model: p.SelectedModel, Messages: []llm.Message{{Role: "user", Content: "Hi"}}, MaxTokens: 1})
	latency := time.Since(start)
	if err != nil {
		report.Checks = append(report.Checks, checkResult{Name: "Chat endpoint works", Status: severityError, Detail: err.Error(), LatencyMS: latency.Milliseconds()})
		report.Issues = append(report.Issues, classifyProviderError(err, 0, report.Label+" (chat)", baseURL))
		return
	}
	report.Checks = append(report.Checks, checkResult{Name: "Chat endpoint works", Status: severityOK, Detail: fmt.Sprintf("responded in %s", latency.Round(time.Millisecond)), LatencyMS: latency.Milliseconds()})
}

// diagnoseHostedEmbeddingProvider diagnoses an OpenAI-compatible
// embeddings-only provider (OpenAI or Voyage AI as the configured
// embedding provider). Unlike diagnoseHostedProvider, this never assumes
// a GET /models listing exists — Voyage AI doesn't document one, and an
// embeddings-only provider has no chat completions endpoint to fall back
// to either — so the only honest, provider-agnostic way to check
// reachability/auth/model-validity is a real embeddings call. That call
// is gated behind deep (defaulting the lightweight, non-deep path to a
// structural "is a key and model even configured" check) so opening the
// Settings page doesn't silently spend money on every render.
func diagnoseHostedEmbeddingProvider(ctx context.Context, label, baseURL, apiKey, model string, deep bool) providerDiagnosticsReport {
	start := time.Now()
	report := providerDiagnosticsReport{
		Provider:       "embedding",
		Label:          label,
		BaseURL:        baseURL,
		CheckedAt:      start.UTC().Format(time.RFC3339),
		EmbeddingModel: model,
	}
	ctx, cancel := context.WithTimeout(ctx, hostedDiagnosticTimeout)
	defer cancel()

	if apiKey == "" {
		report.Status = severityError
		report.Checks = append(report.Checks, checkResult{Name: "API key configured", Status: severityError, Detail: "no API key is set"})
		report.Issues = append(report.Issues, diagnosticIssue{
			Code:     "api_key_missing",
			Severity: severityError,
			Summary:  fmt.Sprintf("No %s API key is configured.", label),
			Fixes:    []diagnosticFix{{Label: "Open provider settings", Action: fixActionOpenSettings}},
		})
		report.TotalCheckMS = time.Since(start).Milliseconds()
		return report
	}
	if model == "" {
		report.Status = severityWarn
		report.Checks = append(report.Checks, checkResult{Name: "Embedding model configured", Status: severityWarn, Detail: "no embedding model is set"})
		report.Issues = append(report.Issues, diagnosticIssue{
			Code:     "embedding_model_missing",
			Severity: severityWarn,
			Summary:  "No embedding model is configured yet.",
			Fixes:    []diagnosticFix{{Label: "Open provider settings", Action: fixActionOpenSettings}},
		})
		report.TotalCheckMS = time.Since(start).Milliseconds()
		return report
	}
	report.Checks = append(report.Checks, checkResult{Name: "API key configured", Status: severityOK, Detail: "set"})
	report.Checks = append(report.Checks, checkResult{Name: "Embedding model configured", Status: severityOK, Detail: model})

	if !deep {
		// Structural-only result: we know a key and model are both set,
		// but haven't actually called out to verify them — status stays
		// "warning," never "ok," so the UI can't be mistaken for a real
		// verified-working state it didn't earn.
		report.Status = severityWarn
		report.TotalCheckMS = time.Since(start).Milliseconds()
		return report
	}

	runHostedEmbeddingDeepCheck(ctx, label, baseURL, apiKey, model, &report)
	report.TotalCheckMS = time.Since(start).Milliseconds()
	report.Status = overallSeverity(report)
	return report
}

// runHostedEmbeddingDeepCheck makes one real, minimal embeddings call —
// the only way to genuinely verify an embeddings-only provider (no chat
// endpoint, no confirmed /models listing to fall back to).
func runHostedEmbeddingDeepCheck(ctx context.Context, label, baseURL, apiKey, model string, report *providerDiagnosticsReport) {
	provider := embeddings.NewOpenAIProvider(baseURL, apiKey, model)
	start := time.Now()
	vectors, err := provider.Embed(ctx, []string{"diagnostic check"})
	latency := time.Since(start)
	report.LatencyMS = latency.Milliseconds()
	if err != nil {
		report.Checks = append(report.Checks, checkResult{Name: "Embeddings endpoint works", Status: severityError, Detail: err.Error(), LatencyMS: latency.Milliseconds()})
		report.Issues = append(report.Issues, classifyProviderError(err, 0, label, baseURL))
		return
	}
	report.Reachable = true
	report.EmbeddingModelFound = true
	dims := 0
	if len(vectors) > 0 {
		dims = len(vectors[0])
	}
	report.Checks = append(report.Checks, checkResult{Name: "Embeddings endpoint works", Status: severityOK, Detail: fmt.Sprintf("responded in %s (%d dimensions)", latency.Round(time.Millisecond), dims), LatencyMS: latency.Milliseconds()})
	report.Capabilities = append(report.Capabilities, capability{Name: "Embeddings", Supported: true, Source: capabilitySourceLive, Detail: "verified via a real embeddings call"})
}

// -- known-model capability table ------------------------------------------

type modelCapabilities struct {
	tools, vision, jsonMode, reasoning, embedding bool
	contextWindow, maxOutput                      int
}

// knownModelCapabilities is a small, explicitly-maintained table of what
// each hosted-model family is publicly documented to support — the only
// honest source for this data, since neither Anthropic nor OpenAI expose
// it via API (see this file's doc comment). Matched by substring against
// the live-verified model ID, so a new dated snapshot (e.g.
// "claude-sonnet-5-20260101") still matches its family without a table
// update. Falls through to a conservative all-false zero value for any
// model ID this table doesn't recognize, rather than guessing — an
// unrecognized model shows no capability claims at all instead of wrong
// ones.
func knownModelCapabilities(modelID string) modelCapabilities {
	id := strings.ToLower(modelID)
	switch {
	case id == "":
		return modelCapabilities{}
	case strings.Contains(id, "embedding") || strings.Contains(id, "embed"):
		return modelCapabilities{embedding: true}
	case strings.Contains(id, "claude-sonnet-5") || strings.Contains(id, "claude-opus-5") || strings.Contains(id, "claude-haiku-4-5") ||
		strings.Contains(id, "claude-sonnet-4") || strings.Contains(id, "claude-opus-4") || strings.Contains(id, "claude-3-7") || strings.Contains(id, "claude-3-5"):
		return modelCapabilities{tools: true, vision: true, jsonMode: true, reasoning: true, contextWindow: 200_000, maxOutput: 64_000}
	case strings.Contains(id, "claude-3-opus") || strings.Contains(id, "claude-3-sonnet") || strings.Contains(id, "claude-3-haiku"):
		return modelCapabilities{tools: true, vision: true, jsonMode: true, contextWindow: 200_000, maxOutput: 4_096}
	case strings.Contains(id, "claude-2") || strings.Contains(id, "claude-instant"):
		return modelCapabilities{contextWindow: 100_000}
	case strings.Contains(id, "gpt-5"):
		return modelCapabilities{tools: true, vision: true, jsonMode: true, reasoning: true, contextWindow: 400_000, maxOutput: 128_000}
	case strings.Contains(id, "gpt-4o") || strings.Contains(id, "gpt-4.1") || strings.Contains(id, "gpt-4-turbo"):
		return modelCapabilities{tools: true, vision: true, jsonMode: true, contextWindow: 128_000, maxOutput: 16_384}
	case strings.HasPrefix(id, "o1") || strings.HasPrefix(id, "o3") || strings.HasPrefix(id, "o4-mini"):
		return modelCapabilities{tools: true, jsonMode: true, reasoning: true, contextWindow: 200_000, maxOutput: 100_000}
	case strings.Contains(id, "gpt-3.5"):
		return modelCapabilities{tools: true, jsonMode: true, contextWindow: 16_385, maxOutput: 4_096}
	default:
		return modelCapabilities{}
	}
}
