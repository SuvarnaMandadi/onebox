package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDiagnoseHostedProviderNoAPIKey(t *testing.T) {
	report := diagnoseHostedProvider(context.Background(), hostedDiagnosticsParams{Kind: hostedOpenAI, BaseURL: "https://api.openai.com/v1"})

	if report.Status != severityError {
		t.Fatalf("Status = %q, want error", report.Status)
	}
	if len(report.Issues) == 0 || report.Issues[0].Code != "api_key_missing" {
		t.Fatalf("expected an api_key_missing issue, got %+v", report.Issues)
	}
}

func TestDiagnoseHostedProviderAuthRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":{"message":"invalid api key"}}`)
	}))
	defer srv.Close()

	report := diagnoseHostedProvider(context.Background(), hostedDiagnosticsParams{Kind: hostedOpenAI, APIKey: "bad-key", BaseURL: srv.URL})

	if report.Status != severityError {
		t.Fatalf("Status = %q, want error", report.Status)
	}
	if len(report.Issues) == 0 || report.Issues[0].Code != "unauthorized" {
		t.Fatalf("expected an unauthorized issue, got %+v", report.Issues)
	}
}

func fakeOpenAIModelsServer(t *testing.T, ids []string, rateLimitRemaining string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Errorf("path = %q, want /models", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
			t.Errorf("Authorization header = %q, want Bearer sk-test", got)
		}
		if rateLimitRemaining != "" {
			w.Header().Set("x-ratelimit-remaining-requests", rateLimitRemaining)
			w.Header().Set("x-ratelimit-limit-requests", "500")
		}
		var data []map[string]string
		for _, id := range ids {
			data = append(data, map[string]string{"id": id})
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"data":[`)
		for i, d := range data {
			if i > 0 {
				fmt.Fprint(w, ",")
			}
			fmt.Fprintf(w, `{"id":%q}`, d["id"])
		}
		fmt.Fprint(w, `]}`)
	}))
}

func TestDiagnoseHostedProviderSuccessWithModelAndRateLimit(t *testing.T) {
	srv := fakeOpenAIModelsServer(t, []string{"gpt-4o-mini", "gpt-4o"}, "487")
	defer srv.Close()

	report := diagnoseHostedProvider(context.Background(), hostedDiagnosticsParams{
		Kind: hostedOpenAI, APIKey: "sk-test", BaseURL: srv.URL, SelectedModel: "gpt-4o-mini",
	})

	if report.Status != severityOK {
		t.Fatalf("Status = %q, want ok — issues: %+v", report.Status, report.Issues)
	}
	if !report.Reachable {
		t.Fatal("Reachable = false, want true")
	}
	if !report.SelectedModelFound {
		t.Fatal("SelectedModelFound = false, want true")
	}
	if len(report.Models) != 2 {
		t.Fatalf("len(Models) = %d, want 2", len(report.Models))
	}

	var sawRateLimitInfo bool
	for _, issue := range report.Issues {
		if issue.Code == "rate_limit_info" {
			sawRateLimitInfo = true
		}
	}
	if !sawRateLimitInfo {
		t.Error("expected a rate_limit_info issue since the fake server sent rate-limit headers")
	}

	var toolCalling capability
	for _, c := range report.Capabilities {
		if c.Name == "Tool Calling" {
			toolCalling = c
		}
	}
	if !toolCalling.Supported {
		t.Error("Tool Calling = false for gpt-4o-mini, want true (known model family)")
	}
	if toolCalling.Source != capabilitySourceKnown {
		t.Errorf("Tool Calling source = %q, want %q — hosted providers have no live capabilities API", toolCalling.Source, capabilitySourceKnown)
	}
}

func TestDiagnoseHostedProviderModelNotListed(t *testing.T) {
	srv := fakeOpenAIModelsServer(t, []string{"gpt-4o-mini"}, "")
	defer srv.Close()

	report := diagnoseHostedProvider(context.Background(), hostedDiagnosticsParams{
		Kind: hostedOpenAI, APIKey: "sk-test", BaseURL: srv.URL, SelectedModel: "gpt-9-ultra",
	})

	if report.SelectedModelFound {
		t.Fatal("SelectedModelFound = true, want false")
	}
	if report.Status != severityError {
		t.Fatalf("Status = %q, want error", report.Status)
	}
	var found bool
	for _, issue := range report.Issues {
		if issue.Code == "model_missing" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected a model_missing issue")
	}
}

func TestDiagnoseHostedProviderRateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"error":{"message":"rate limited"}}`)
	}))
	defer srv.Close()

	report := diagnoseHostedProvider(context.Background(), hostedDiagnosticsParams{Kind: hostedOpenAI, APIKey: "sk-test", BaseURL: srv.URL})

	if len(report.Issues) == 0 || report.Issues[0].Code != "rate_limited" {
		t.Fatalf("expected a rate_limited issue, got %+v", report.Issues)
	}
	if report.Issues[0].Severity != severityWarn {
		t.Errorf("rate-limited severity = %q, want warning (transient, not a real misconfiguration)", report.Issues[0].Severity)
	}
}

func TestDiagnoseHostedProviderInvalidBaseURL(t *testing.T) {
	report := diagnoseHostedProvider(context.Background(), hostedDiagnosticsParams{
		Kind: hostedOpenAI, APIKey: "sk-test", BaseURL: "not-a-url",
	})
	if report.Status != severityError {
		t.Fatalf("Status = %q, want error for a garbage base URL", report.Status)
	}
	if len(report.Issues) == 0 {
		t.Fatal("expected at least one issue explaining the bad URL")
	}
}

// -- embedding provider (no chat endpoint) -----------------------------

func TestDiagnoseHostedEmbeddingProviderMissingKey(t *testing.T) {
	report := diagnoseHostedEmbeddingProvider(context.Background(), "Voyage AI", "https://api.voyageai.com/v1", "", "voyage-3", true)
	if report.Status != severityError {
		t.Fatalf("Status = %q, want error", report.Status)
	}
	if len(report.Issues) == 0 || report.Issues[0].Code != "api_key_missing" {
		t.Fatalf("expected api_key_missing, got %+v", report.Issues)
	}
}

func TestDiagnoseHostedEmbeddingProviderNonDeepStaysUnverified(t *testing.T) {
	// deep=false must never claim "ok" — it hasn't actually called out.
	report := diagnoseHostedEmbeddingProvider(context.Background(), "Voyage AI", "https://api.voyageai.com/v1", "vk-test", "voyage-3", false)
	if report.Status == severityOK {
		t.Fatal("Status = ok for a non-deep check that made no network call — must not claim verified success")
	}
	if report.EmbeddingModelFound {
		t.Fatal("EmbeddingModelFound = true without ever calling the provider")
	}
}

func fakeEmbeddingServer(t *testing.T, dims int, fail bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"error":{"message":"invalid api key"}}`)
			return
		}
		vec := make([]string, dims)
		for i := range vec {
			vec[i] = "0.1"
		}
		fmt.Fprintf(w, `{"data":[{"embedding":[%s],"index":0}]}`, joinFloats(vec))
	}))
}

func joinFloats(vals []string) string {
	out := ""
	for i, v := range vals {
		if i > 0 {
			out += ","
		}
		out += v
	}
	return out
}

func TestDiagnoseHostedEmbeddingProviderDeepSuccess(t *testing.T) {
	srv := fakeEmbeddingServer(t, 1024, false)
	defer srv.Close()

	report := diagnoseHostedEmbeddingProvider(context.Background(), "OpenAI", srv.URL, "sk-test", "text-embedding-3-small", true)

	if report.Status != severityOK {
		t.Fatalf("Status = %q, want ok — issues: %+v", report.Status, report.Issues)
	}
	if !report.EmbeddingModelFound {
		t.Fatal("EmbeddingModelFound = false, want true after a real successful embed call")
	}
	var found bool
	for _, c := range report.Capabilities {
		if c.Name == "Embeddings" && c.Supported && c.Source == capabilitySourceLive {
			found = true
		}
	}
	if !found {
		t.Error("expected a live-verified Embeddings capability")
	}
}

func TestDiagnoseHostedEmbeddingProviderDeepFailure(t *testing.T) {
	srv := fakeEmbeddingServer(t, 0, true)
	defer srv.Close()

	report := diagnoseHostedEmbeddingProvider(context.Background(), "OpenAI", srv.URL, "sk-bad", "text-embedding-3-small", true)

	if report.Status != severityError {
		t.Fatalf("Status = %q, want error", report.Status)
	}
	if report.EmbeddingModelFound {
		t.Fatal("EmbeddingModelFound = true despite the embed call failing")
	}
}

func TestKnownModelCapabilitiesUnknownModelHasNoClaims(t *testing.T) {
	caps := knownModelCapabilities("some-totally-unheard-of-model-xyz")
	if caps.tools || caps.vision || caps.jsonMode || caps.reasoning {
		t.Fatalf("caps = %+v, want all false for an unrecognized model — no guessing", caps)
	}
}

func TestKnownModelCapabilitiesClaudeSonnet5(t *testing.T) {
	caps := knownModelCapabilities("claude-sonnet-5-20260101")
	if !caps.tools || !caps.vision || !caps.reasoning {
		t.Fatalf("caps = %+v, want tools/vision/reasoning true for the claude-sonnet-5 family", caps)
	}
	if caps.contextWindow != 200_000 {
		t.Fatalf("contextWindow = %d, want 200000", caps.contextWindow)
	}
}
