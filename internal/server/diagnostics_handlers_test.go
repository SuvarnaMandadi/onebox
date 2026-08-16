package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func fakeOllamaDiagnosticsServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/version":
			w.Write([]byte(`{"version":"0.32.1"}`))
		case "/api/tags":
			w.Write([]byte(realisticTagsResponse))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestHandleProviderDiagnosticsOllamaEndToEnd(t *testing.T) {
	fake := fakeOllamaDiagnosticsServer(t)
	defer fake.Close()

	srv, _ := newTestServer(t)
	adminToken := bootstrapAdmin(t, srv)

	rec := doAuth(t, srv, http.MethodPost, "/api/settings/diagnostics", adminToken, diagnosticsRequest{
		Provider: "ollama", BaseURL: fake.URL, Model: "llama3.2:3b",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	var report providerDiagnosticsReport
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if report.Status != severityOK {
		t.Fatalf("Status = %q, want ok", report.Status)
	}
	if !report.SelectedModelFound {
		t.Fatal("SelectedModelFound = false, want true")
	}

	// The run must have been recorded to Connection History.
	histRec := doAuth(t, srv, http.MethodGet, "/api/settings/diagnostics/history?provider=ollama", adminToken, nil)
	if histRec.Code != http.StatusOK {
		t.Fatalf("history status = %d, want 200", histRec.Code)
	}
	var hist struct {
		Items []diagnosticsHistoryEntry `json:"items"`
	}
	json.Unmarshal(histRec.Body.Bytes(), &hist)
	if len(hist.Items) != 1 || !hist.Items[0].Success {
		t.Fatalf("history = %+v, want exactly one successful entry", hist.Items)
	}
}

func TestHandleProviderDiagnosticsRejectsUnknownProvider(t *testing.T) {
	srv, _ := newTestServer(t)
	adminToken := bootstrapAdmin(t, srv)

	rec := doAuth(t, srv, http.MethodPost, "/api/settings/diagnostics", adminToken, diagnosticsRequest{Provider: "not-a-real-provider"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandleProviderDiagnosticsRequiresAdmin(t *testing.T) {
	srv, _ := newTestServer(t)
	_, userToken := signupUser(t, srv, "notadmin@example.com")

	rec := doAuth(t, srv, http.MethodPost, "/api/settings/diagnostics", userToken, diagnosticsRequest{Provider: "ollama"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestHandleProviderDiagnosticsAnthropicMissingKey(t *testing.T) {
	srv, _ := newTestServer(t)
	adminToken := bootstrapAdmin(t, srv)

	rec := doAuth(t, srv, http.MethodPost, "/api/settings/diagnostics", adminToken, diagnosticsRequest{Provider: "anthropic"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (the diagnostic itself succeeds at running, even though it reports a failure)", rec.Code)
	}
	var report providerDiagnosticsReport
	json.Unmarshal(rec.Body.Bytes(), &report)
	if report.Status != severityError {
		t.Fatalf("Status = %q, want error — no API key is configured", report.Status)
	}
}

func TestHandleDiagnosticsHistoryRequiresAdmin(t *testing.T) {
	srv, _ := newTestServer(t)
	rec := doAuth(t, srv, http.MethodGet, "/api/settings/diagnostics/history", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestHandleProviderPerformanceRequiresProviderParam(t *testing.T) {
	srv, _ := newTestServer(t)
	adminToken := bootstrapAdmin(t, srv)
	rec := doAuth(t, srv, http.MethodGet, "/api/settings/performance", adminToken, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 without a provider query param", rec.Code)
	}
}

func TestHandleValidateSettingsCatchesBadURLAndMissingKey(t *testing.T) {
	srv, _ := newTestServer(t)
	adminToken := bootstrapAdmin(t, srv)

	rec := doAuth(t, srv, http.MethodPost, "/api/settings/validate", adminToken, map[string]string{
		"ollama_base_url": "not a url at all",
		"chat_provider":   "anthropic",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Issues []configIssue `json:"issues"`
		Valid  bool          `json:"valid"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Valid {
		t.Fatal("Valid = true, want false — the URL is garbage and no Anthropic key is set")
	}
	var sawURLIssue, sawKeyIssue bool
	for _, issue := range resp.Issues {
		if issue.Field == "ollama_base_url" {
			sawURLIssue = true
		}
		if strings.Contains(issue.Message, "Anthropic API key") {
			sawKeyIssue = true
		}
	}
	if !sawURLIssue {
		t.Error("expected an issue about the invalid Ollama base URL")
	}
	if !sawKeyIssue {
		t.Error("expected an issue about the missing Anthropic API key")
	}
}

func TestHandleValidateSettingsCleanConfigHasNoIssues(t *testing.T) {
	srv, _ := newTestServer(t)
	adminToken := bootstrapAdmin(t, srv)

	rec := doAuth(t, srv, http.MethodPost, "/api/settings/validate", adminToken, map[string]string{
		"chat_provider":   "ollama",
		"chat_model":      "llama3.2:3b",
		"ollama_base_url": "http://localhost:11434",
	})
	var resp struct {
		Issues []configIssue `json:"issues"`
		Valid  bool          `json:"valid"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if !resp.Valid {
		t.Fatalf("Valid = false, want true — this is a clean, self-consistent config; issues: %+v", resp.Issues)
	}
}

func TestHandleValidateSettingsCatchesEmbeddingModelAsChatModel(t *testing.T) {
	srv, _ := newTestServer(t)
	adminToken := bootstrapAdmin(t, srv)

	rec := doAuth(t, srv, http.MethodPost, "/api/settings/validate", adminToken, map[string]string{
		"chat_provider": "ollama",
		"chat_model":    "nomic-embed-text",
	})
	var resp struct {
		Issues []configIssue `json:"issues"`
		Valid  bool          `json:"valid"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Valid {
		t.Fatal("Valid = true, want false — nomic-embed-text is an embedding model, not a chat model")
	}
}

func TestHandleOllamaPullStreamsRealProgress(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/pull" {
			t.Errorf("path = %q, want /api/pull", r.URL.Path)
		}
		flusher := w.(http.Flusher)
		w.Write([]byte(`{"status":"pulling manifest"}` + "\n"))
		flusher.Flush()
		w.Write([]byte(`{"status":"success"}` + "\n"))
		flusher.Flush()
	}))
	defer fake.Close()

	srv, _ := newTestServer(t)
	adminToken := bootstrapAdmin(t, srv)

	rec := doAuth(t, srv, http.MethodPost, "/api/settings/ollama-pull", adminToken, ollamaPullRequestBody{
		BaseURL: fake.URL, Model: "llama3.2:3b",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "pulling manifest") {
		t.Error("expected the real \"pulling manifest\" status to be relayed")
	}
	if !strings.Contains(body, `"done":true`) {
		t.Error("expected a final done:true event")
	}
}

func TestHandleOllamaPullRejectsEmptyModel(t *testing.T) {
	srv, _ := newTestServer(t)
	adminToken := bootstrapAdmin(t, srv)

	rec := doAuth(t, srv, http.MethodPost, "/api/settings/ollama-pull", adminToken, ollamaPullRequestBody{})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for an empty model name", rec.Code)
	}
}
