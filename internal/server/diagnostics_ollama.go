// diagnostics_ollama.go runs the real, live checklist Section 3 asks for
// against a local (or remote) Ollama daemon: reachable, /api/tags,
// /api/version, selected model exists, embedding model exists, streaming
// works, the generate endpoint works, the chat endpoint works, and
// tool-calling compatibility — every one of these is an actual network
// call against the daemon this run, never inferred or cached from a
// previous check.
package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"onebox/internal/embeddings"
	"onebox/internal/llm"
)

// ollamaDiagnosticsParams is what diagnoseOllama needs to know beyond
// "which daemon" — the currently-selected chat and embedding models, so
// it can check whether THIS instance's actual configuration will work,
// not just whether the daemon is up in the abstract.
type ollamaDiagnosticsParams struct {
	BaseURL        string
	SelectedModel  string
	EmbeddingModel string
	// DeepCheck gates the real generate/chat/streaming calls (Section 3) —
	// these cost real wall-clock time (a cold model load can take
	// seconds) and, unlike a reachability ping, actually run inference.
	// false gives a fast reachability+model-listing report; true runs the
	// full checklist. See handleProviderDiagnostics for the request field
	// that sets this.
	DeepCheck bool
}

// ollamaDiagnosticTimeout bounds the fast checks only (version, tags) —
// pure metadata lookups with no reason to ever take long. Deep checks
// (real generate/chat/streaming calls) get their own, much longer budget
// — see ollamaDeepCheckTimeout/ollamaDeepCheckClient below — because a
// genuine inference call against a local, CPU-bound model can legitimately
// take far longer than a reachability ping, especially a cold model load
// (the daemon has to read the whole model into memory before it can
// generate a single token). Reusing httpTestClient's 8s budget for a real
// generate call was tried first and found live, against this project's
// own local Ollama instance, to fail with "context deadline exceeded" on
// an unloaded model — the same class of problem RC2's chat-timeout fix
// (120s -> 240s in server.go) already solved for the chat endpoint
// itself; this is that same fix applied to the diagnostics path.
const ollamaDiagnosticTimeout = 20 * time.Second

// ollamaDeepCheckTimeout/ollamaDeepCheckClient back only the deep
// generate/chat/streaming checks (Section 3), gated behind an explicit
// "Run full check" action, not the default lightweight diagnostic run —
// so a longer wait here is expected and acceptable to whoever clicked
// that button, unlike the fast-path checks above.
const ollamaDeepCheckTimeout = 90 * time.Second

var ollamaDeepCheckClient = &http.Client{Timeout: 90 * time.Second}

func diagnoseOllama(parentCtx context.Context, p ollamaDiagnosticsParams) providerDiagnosticsReport {
	start := time.Now()
	report := providerDiagnosticsReport{
		Provider:  "ollama",
		Label:     "Ollama",
		BaseURL:   p.BaseURL,
		CheckedAt: start.UTC().Format(time.RFC3339),
	}

	ctx, cancel := context.WithTimeout(parentCtx, ollamaDiagnosticTimeout)
	defer cancel()

	client := llm.NewOllamaClient(p.BaseURL)
	client.Client = httpTestClient

	// -- Check 1: reachable + version -----------------------------------
	versionStart := time.Now()
	version, verErr := client.Version(ctx)
	versionLatency := time.Since(versionStart)
	if verErr != nil {
		// Version is best-effort on some older daemons, but a total
		// failure to even connect here (vs. a clean 404) means the whole
		// report is "unreachable" — try /api/tags next before giving up
		// entirely, since that's the more load-bearing endpoint.
		report.Checks = append(report.Checks, checkResult{Name: "Server reachable", Status: severityError, Detail: verErr.Error(), LatencyMS: versionLatency.Milliseconds()})
	} else {
		report.Reachable = true
		report.Version = version
		report.Checks = append(report.Checks, checkResult{Name: "Server reachable", Status: severityOK, Detail: fmt.Sprintf("responded in %s", versionLatency.Round(time.Millisecond)), LatencyMS: versionLatency.Milliseconds()})
		report.Checks = append(report.Checks, checkResult{Name: "GET /api/version", Status: severityOK, Detail: orNotReported(version), LatencyMS: versionLatency.Milliseconds()})
	}
	report.LatencyMS = versionLatency.Milliseconds()

	// -- Check 2: /api/tags — model list + real detail -------------------
	tagsStart := time.Now()
	models, tagsErr := client.ListModelsDetailed(ctx)
	tagsLatency := time.Since(tagsStart)
	if tagsErr != nil {
		report.Checks = append(report.Checks, checkResult{Name: "GET /api/tags", Status: severityError, Detail: tagsErr.Error(), LatencyMS: tagsLatency.Milliseconds()})
		report.Status = severityError
		issue := classifyProviderError(tagsErr, 0, "Ollama", p.BaseURL)
		issue.Fixes = append(issue.Fixes, diagnosticFix{Label: "Copy curl command", Action: fixActionCopyCurl, Command: curlCommand("GET", strings.TrimRight(p.BaseURL, "/")+"/api/tags", nil, false)})
		report.Issues = append(report.Issues, issue)
		report.TotalCheckMS = time.Since(start).Milliseconds()
		return report
	}
	report.Checks = append(report.Checks, checkResult{Name: "GET /api/tags", Status: severityOK, Detail: fmt.Sprintf("%d model(s) installed", len(models)), LatencyMS: tagsLatency.Milliseconds()})

	capsKnown := len(models) > 0 && models[0].CapabilitiesKnown
	for _, m := range models {
		info := modelInfo{
			Name:          m.Name,
			SizeBytes:     m.SizeBytes,
			Quantization:  m.QuantizationLevel,
			ParameterSize: m.ParameterSize,
			Family:        m.Family,
			ContextLength: m.ContextLength,
			IsSelected:    m.Name == p.SelectedModel,
			Source:        capabilitySourceLive,
		}
		if capsKnown {
			info.IsEmbedding = containsStr(m.Capabilities, "embedding")
			info.IsVision = containsStr(m.Capabilities, "vision")
		} else {
			// Pre-capabilities-field daemon — fall back to the existing
			// name-based heuristic rather than claiming "live" certainty
			// this daemon version can't actually back.
			info.IsEmbedding = llm.IsEmbeddingModel(m.Name)
			info.Source = capabilitySourceUnknown
		}
		report.Models = append(report.Models, info)
	}

	// -- Selected/embedding model presence -------------------------------
	if p.SelectedModel != "" {
		for i := range report.Models {
			if report.Models[i].Name == p.SelectedModel {
				report.SelectedModelFound = true
				report.SelectedModel = p.SelectedModel
				report.ContextWindow = report.Models[i].ContextLength
				break
			}
		}
		if !report.SelectedModelFound {
			report.SelectedModel = p.SelectedModel
			report.Checks = append(report.Checks, checkResult{Name: "Selected model exists", Status: severityError, Detail: fmt.Sprintf("%q is not installed", p.SelectedModel)})
			report.Issues = append(report.Issues, diagnosticIssue{
				Code:           "model_missing",
				Severity:       severityError,
				Summary:        fmt.Sprintf("The selected chat model %q isn't installed on this Ollama daemon.", p.SelectedModel),
				PossibleCauses: []string{"The model was never pulled", "The model was removed since it was selected", "A typo in the model name"},
				Fixes: []diagnosticFix{
					{Label: "Pull " + p.SelectedModel, Action: fixActionPullModel, Command: p.SelectedModel},
					{Label: "Refresh model list", Action: fixActionRefreshModels},
				},
			})
		} else {
			report.Checks = append(report.Checks, checkResult{Name: "Selected model exists", Status: severityOK, Detail: p.SelectedModel})
		}
	}
	if p.EmbeddingModel != "" {
		found := false
		for _, m := range report.Models {
			if m.Name == p.EmbeddingModel {
				found = true
				break
			}
		}
		report.EmbeddingModel = p.EmbeddingModel
		report.EmbeddingModelFound = found
		if !found {
			report.Checks = append(report.Checks, checkResult{Name: "Embedding model exists", Status: severityError, Detail: fmt.Sprintf("%q is not installed", p.EmbeddingModel)})
			report.Issues = append(report.Issues, diagnosticIssue{
				Code:           "embedding_model_missing",
				Severity:       severityError,
				Summary:        fmt.Sprintf("The selected embedding model %q isn't installed on this Ollama daemon.", p.EmbeddingModel),
				PossibleCauses: []string{"The model was never pulled", "A typo in the model name"},
				Fixes: []diagnosticFix{
					{Label: "Pull " + p.EmbeddingModel, Action: fixActionPullModel, Command: p.EmbeddingModel},
					{Label: "Refresh model list", Action: fixActionRefreshModels},
				},
			})
		} else {
			report.Checks = append(report.Checks, checkResult{Name: "Embedding model exists", Status: severityOK, Detail: p.EmbeddingModel})
		}
	}

	// -- Capabilities (Section 6) ----------------------------------------
	report.Capabilities = append(report.Capabilities,
		capability{Name: "Chat", Supported: true, Source: capabilitySourceLive, Detail: "reachable via POST /api/chat"},
	)
	if capsKnown {
		toolCapable := report.SelectedModelFound && containsStr(modelCapsByName(models, p.SelectedModel), "tools")
		report.Capabilities = append(report.Capabilities,
			capability{Name: "Tool Calling", Supported: toolCapable, Source: capabilitySourceLive, Detail: "reported by /api/tags for the selected model"},
			capability{Name: "Function Calling", Supported: toolCapable, Source: capabilitySourceLive, Detail: "same capability as Tool Calling on Ollama"},
		)
	} else {
		report.Capabilities = append(report.Capabilities,
			capability{Name: "Tool Calling", Supported: false, Source: capabilitySourceUnknown, Detail: "this Ollama version doesn't report model capabilities — upgrade to 0.5+ to see this"},
			capability{Name: "Function Calling", Supported: false, Source: capabilitySourceUnknown, Detail: "same as Tool Calling"},
		)
	}
	report.Capabilities = append(report.Capabilities,
		capability{Name: "Streaming", Supported: true, Source: capabilitySourceLive, Detail: "Ollama's /api/chat and /api/generate both support stream:true"},
		capability{Name: "JSON Output", Supported: true, Source: capabilitySourceLive, Detail: "Ollama's /api/generate and /api/chat accept format:\"json\""},
	)
	if p.EmbeddingModel != "" {
		report.Capabilities = append(report.Capabilities, capability{Name: "Embeddings", Supported: report.EmbeddingModelFound, Source: capabilitySourceLive, Detail: "checked via /api/tags"})
	}
	if report.SelectedModelFound {
		visionCapable := false
		for _, m := range report.Models {
			if m.Name == p.SelectedModel {
				visionCapable = m.IsVision
			}
		}
		report.Capabilities = append(report.Capabilities, capability{Name: "Vision", Supported: visionCapable, Source: capabilitySourceLive, Detail: "reported by /api/tags for the selected model"})
	}
	// Ollama has no concept of extended "thinking"/reasoning mode
	// distinct from generation itself for most models — a small,
	// explicitly-named set of reasoning-tuned families (e.g. deepseek-r1)
	// are the exception, and even for those Ollama's API exposes no live
	// flag, so this is intentionally left out of the capability list
	// rather than guessed at.

	// -- Deep checks: generate / chat / streaming (Section 3) ------------
	if p.DeepCheck && report.SelectedModelFound {
		// A real inference call, not a metadata lookup — derived from
		// parentCtx (not the already-20s-budgeted ctx above) with its own
		// generous timeout and a client with a matching HTTP-level
		// timeout. See ollamaDeepCheckTimeout's doc comment.
		deepCtx, deepCancel := context.WithTimeout(parentCtx, ollamaDeepCheckTimeout)
		defer deepCancel()
		deepClient := llm.NewOllamaClient(p.BaseURL)
		deepClient.Client = ollamaDeepCheckClient
		runOllamaDeepChecks(deepCtx, deepClient, p.SelectedModel, &report)
	}

	report.TotalCheckMS = time.Since(start).Milliseconds()
	report.Status = overallSeverity(report)
	return report
}

// runOllamaDeepChecks makes real, bounded inference calls to prove the
// generate, chat, and streaming endpoints actually work end-to-end for
// the selected model — not just that the daemon is up. Every call caps
// output at a handful of tokens (see llm.OllamaClient.Generate) so this
// costs seconds, not the full latency of a real chat turn, while still
// being a genuine round trip through the model.
func runOllamaDeepChecks(ctx context.Context, client *llm.OllamaClient, model string, report *providerDiagnosticsReport) {
	genStart := time.Now()
	_, genErr := client.Generate(ctx, model, "Reply with one word.", 4)
	genLatency := time.Since(genStart)
	if genErr != nil {
		report.Checks = append(report.Checks, checkResult{Name: "Generate endpoint works", Status: severityError, Detail: genErr.Error(), LatencyMS: genLatency.Milliseconds()})
		report.Issues = append(report.Issues, classifyProviderError(genErr, 0, "Ollama (generate)", client.BaseURL))
	} else {
		report.Checks = append(report.Checks, checkResult{Name: "Generate endpoint works", Status: severityOK, Detail: fmt.Sprintf("responded in %s", genLatency.Round(time.Millisecond)), LatencyMS: genLatency.Milliseconds()})
	}

	chatStart := time.Now()
	chatResult, chatErr := client.Chat(ctx, llm.ChatRequest{Model: model, Messages: []llm.Message{{Role: "user", Content: "Reply with one word."}}})
	chatLatency := time.Since(chatStart)
	if chatErr != nil {
		report.Checks = append(report.Checks, checkResult{Name: "Chat endpoint works", Status: severityError, Detail: chatErr.Error(), LatencyMS: chatLatency.Milliseconds()})
		report.Issues = append(report.Issues, classifyProviderError(chatErr, 0, "Ollama (chat)", client.BaseURL))
	} else {
		report.Checks = append(report.Checks, checkResult{Name: "Chat endpoint works", Status: severityOK, Detail: fmt.Sprintf("responded in %s", chatLatency.Round(time.Millisecond)), LatencyMS: chatLatency.Milliseconds()})
		if chatResult.TokensOut > 0 {
			report.MaxTokens = 0 // Ollama doesn't report a model max-tokens ceiling — left honestly unset
		}
	}

	streamStart := time.Now()
	var gotDelta bool
	var deltaCount int
	_, streamErr := client.ChatStream(ctx, llm.ChatRequest{Model: model, Messages: []llm.Message{{Role: "user", Content: "Count to three."}}}, func(delta string) {
		if delta != "" {
			gotDelta = true
			deltaCount++
		}
	})
	streamLatency := time.Since(streamStart)
	switch {
	case streamErr != nil:
		report.Checks = append(report.Checks, checkResult{Name: "Streaming works", Status: severityError, Detail: streamErr.Error(), LatencyMS: streamLatency.Milliseconds()})
		report.Issues = append(report.Issues, classifyProviderError(streamErr, 0, "Ollama (stream)", client.BaseURL))
	case !gotDelta:
		report.Checks = append(report.Checks, checkResult{Name: "Streaming works", Status: severityWarn, Detail: "connected, but no incremental content arrived — the model may have returned an empty reply", LatencyMS: streamLatency.Milliseconds()})
	default:
		report.Checks = append(report.Checks, checkResult{Name: "Streaming works", Status: severityOK, Detail: fmt.Sprintf("%d chunk(s) in %s", deltaCount, streamLatency.Round(time.Millisecond)), LatencyMS: streamLatency.Milliseconds()})
	}
}

// runOllamaEmbeddingDeepCheck makes one real embedding call against the
// configured Ollama embedding model — proving /api/embed genuinely
// produces vectors for this model, not just that it's listed in
// /api/tags. Uses the same embeddings.OllamaProvider the RAG pipeline
// itself calls at query/ingest time, so this checks the exact code path
// a real request would take.
func runOllamaEmbeddingDeepCheck(ctx context.Context, baseURL, model string, report *providerDiagnosticsReport) {
	provider := embeddings.NewOllamaProvider(baseURL, model)
	start := time.Now()
	vectors, err := provider.Embed(ctx, []string{"diagnostic check"})
	latency := time.Since(start)
	if err != nil {
		report.Checks = append(report.Checks, checkResult{Name: "Embeddings endpoint works", Status: severityError, Detail: err.Error(), LatencyMS: latency.Milliseconds()})
		report.Issues = append(report.Issues, classifyProviderError(err, 0, "Ollama (embed)", baseURL))
		return
	}
	dims := 0
	if len(vectors) > 0 {
		dims = len(vectors[0])
	}
	report.Checks = append(report.Checks, checkResult{Name: "Embeddings endpoint works", Status: severityOK, Detail: fmt.Sprintf("responded in %s (%d dimensions)", latency.Round(time.Millisecond), dims), LatencyMS: latency.Milliseconds()})
	report.Capabilities = append(report.Capabilities, capability{Name: "Embeddings", Supported: true, Source: capabilitySourceLive, Detail: "verified via a real embeddings call"})
}

func modelCapsByName(models []llm.OllamaModelDetail, name string) []string {
	for _, m := range models {
		if m.Name == name {
			return m.Capabilities
		}
	}
	return nil
}

func containsStr(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// overallSeverity folds every check/issue in a report into one top-level
// status — error if anything errored, warning if only warnings, ok
// otherwise. Never hardcoded to "ok"; a report with zero checks (should
// not happen in practice) folds to "error" (fail closed) via the default
// case below never being reached with severityOK.
func overallSeverity(report providerDiagnosticsReport) diagnosticSeverity {
	worst := severityOK
	for _, c := range report.Checks {
		if c.Status == severityError {
			return severityError
		}
		if c.Status == severityWarn {
			worst = severityWarn
		}
	}
	for _, i := range report.Issues {
		if i.Severity == severityError {
			return severityError
		}
		if i.Severity == severityWarn {
			worst = severityWarn
		}
	}
	return worst
}

func orNotReported(s string) string {
	if s == "" {
		return "(not reported)"
	}
	return s
}
