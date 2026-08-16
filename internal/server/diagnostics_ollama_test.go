package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeOllamaServer builds a real httptest.Server that mimics Ollama's
// actual wire shapes (grounded against a live 0.32.1 daemon — see
// diagnostics_ollama.go's doc comment), so diagnoseOllama is exercised
// against real HTTP round trips, never mocked at the Go interface level.
func fakeOllamaServer(t *testing.T, opts struct {
	version    string
	tags       string
	generateOK bool
	chatOK     bool
}) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/version":
			fmt.Fprintf(w, `{"version":%q}`, opts.version)
		case "/api/tags":
			fmt.Fprint(w, opts.tags)
		case "/api/generate":
			if !opts.generateOK {
				w.WriteHeader(http.StatusInternalServerError)
				fmt.Fprint(w, `{"error":"generate failed"}`)
				return
			}
			fmt.Fprint(w, `{"response":"ok","done":true}`)
		case "/api/chat":
			if !opts.chatOK {
				w.WriteHeader(http.StatusInternalServerError)
				fmt.Fprint(w, `{"error":"chat failed"}`)
				return
			}
			fmt.Fprint(w, `{"message":{"content":"ok"},"done":true,"prompt_eval_count":3,"eval_count":1}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

const realisticTagsResponse = `{"models":[
	{"name":"llama3.2:3b","size":2019393189,"details":{"family":"llama","families":["llama"],"parameter_size":"3.2B","quantization_level":"Q4_K_M","context_length":131072,"embedding_length":3072},"capabilities":["completion","tools"]},
	{"name":"nomic-embed-text:latest","size":274302450,"details":{"family":"nomic-bert","families":["nomic-bert"],"parameter_size":"137M","quantization_level":"F16","context_length":2048,"embedding_length":768},"capabilities":["embedding"]}
]}`

func TestDiagnoseOllamaHealthyInstance(t *testing.T) {
	srv := fakeOllamaServer(t, struct {
		version    string
		tags       string
		generateOK bool
		chatOK     bool
	}{version: "0.32.1", tags: realisticTagsResponse, generateOK: true, chatOK: true})
	defer srv.Close()

	report := diagnoseOllama(context.Background(), ollamaDiagnosticsParams{
		BaseURL:        srv.URL,
		SelectedModel:  "llama3.2:3b",
		EmbeddingModel: "nomic-embed-text:latest",
	})

	if !report.Reachable {
		t.Fatal("Reachable = false, want true")
	}
	if report.Status != severityOK {
		t.Fatalf("Status = %q, want ok — issues: %+v", report.Status, report.Issues)
	}
	if report.Version != "0.32.1" {
		t.Fatalf("Version = %q, want 0.32.1", report.Version)
	}
	if !report.SelectedModelFound {
		t.Fatal("SelectedModelFound = false, want true — llama3.2:3b is in the fake /api/tags response")
	}
	if !report.EmbeddingModelFound {
		t.Fatal("EmbeddingModelFound = false, want true")
	}
	if report.ContextWindow != 131072 {
		t.Fatalf("ContextWindow = %d, want 131072 (real value from /api/tags)", report.ContextWindow)
	}
	if len(report.Models) != 2 {
		t.Fatalf("len(Models) = %d, want 2", len(report.Models))
	}

	var toolCalling, embeddings bool
	for _, c := range report.Capabilities {
		if c.Name == "Tool Calling" {
			toolCalling = c.Supported
			if c.Source != capabilitySourceLive {
				t.Errorf("Tool Calling capability source = %q, want %q (real /api/tags capabilities field)", c.Source, capabilitySourceLive)
			}
		}
		if c.Name == "Embeddings" {
			embeddings = c.Supported
		}
	}
	if !toolCalling {
		t.Error("Tool Calling capability = false, want true — llama3.2:3b reports \"tools\" in its capabilities array")
	}
	if !embeddings {
		t.Error("Embeddings capability = false, want true — embedding model is installed")
	}
}

func TestDiagnoseOllamaUnreachable(t *testing.T) {
	report := diagnoseOllama(context.Background(), ollamaDiagnosticsParams{BaseURL: "http://127.0.0.1:1"})

	if report.Reachable {
		t.Fatal("Reachable = true, want false for a port nothing listens on")
	}
	if report.Status != severityError {
		t.Fatalf("Status = %q, want error", report.Status)
	}
	if len(report.Issues) == 0 {
		t.Fatal("expected at least one diagnosticIssue explaining the failure")
	}
	if report.Issues[0].Code != "connection_refused" {
		t.Errorf("issue code = %q, want connection_refused", report.Issues[0].Code)
	}
	if len(report.Issues[0].PossibleCauses) == 0 {
		t.Error("expected PossibleCauses to be populated — never a bare failure message")
	}
	if len(report.Issues[0].Fixes) == 0 {
		t.Error("expected at least one suggested Fix")
	}
}

func TestDiagnoseOllamaSelectedModelMissing(t *testing.T) {
	srv := fakeOllamaServer(t, struct {
		version    string
		tags       string
		generateOK bool
		chatOK     bool
	}{version: "0.32.1", tags: realisticTagsResponse, generateOK: true, chatOK: true})
	defer srv.Close()

	report := diagnoseOllama(context.Background(), ollamaDiagnosticsParams{
		BaseURL:       srv.URL,
		SelectedModel: "does-not-exist:latest",
	})

	if report.SelectedModelFound {
		t.Fatal("SelectedModelFound = true, want false")
	}
	if report.Status != severityError {
		t.Fatalf("Status = %q, want error — a missing selected model is a real problem", report.Status)
	}
	var found bool
	for _, issue := range report.Issues {
		if issue.Code == "model_missing" {
			found = true
			var hasPull bool
			for _, f := range issue.Fixes {
				if f.Action == fixActionPullModel && f.Command == "does-not-exist:latest" {
					hasPull = true
				}
			}
			if !hasPull {
				t.Error("expected a pull_model fix naming the exact missing model")
			}
		}
	}
	if !found {
		t.Fatal("expected a model_missing issue")
	}
}

func TestDiagnoseOllamaEmbeddingModelMissing(t *testing.T) {
	srv := fakeOllamaServer(t, struct {
		version    string
		tags       string
		generateOK bool
		chatOK     bool
	}{version: "0.32.1", tags: realisticTagsResponse, generateOK: true, chatOK: true})
	defer srv.Close()

	report := diagnoseOllama(context.Background(), ollamaDiagnosticsParams{
		BaseURL:        srv.URL,
		SelectedModel:  "llama3.2:3b",
		EmbeddingModel: "mystery-embed:latest",
	})

	if report.EmbeddingModelFound {
		t.Fatal("EmbeddingModelFound = true, want false")
	}
	var found bool
	for _, issue := range report.Issues {
		if issue.Code == "embedding_model_missing" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected an embedding_model_missing issue")
	}
}

func TestDiagnoseOllamaOlderDaemonWithoutCapabilities(t *testing.T) {
	srv := fakeOllamaServer(t, struct {
		version    string
		tags       string
		generateOK bool
		chatOK     bool
	}{version: "0.3.0", tags: `{"models":[{"name":"llama3.2:3b"}]}`, generateOK: true, chatOK: true})
	defer srv.Close()

	report := diagnoseOllama(context.Background(), ollamaDiagnosticsParams{BaseURL: srv.URL, SelectedModel: "llama3.2:3b"})

	for _, c := range report.Capabilities {
		if c.Name == "Tool Calling" {
			if c.Source != capabilitySourceUnknown {
				t.Errorf("Tool Calling source = %q, want %q — this daemon version reports no capabilities array, so this must not be claimed as live-verified", c.Source, capabilitySourceUnknown)
			}
			if c.Supported {
				t.Error("Tool Calling = true for an older daemon with unknown capabilities — must not guess yes")
			}
		}
	}
}

func TestDiagnoseOllamaDeepCheckRunsRealCalls(t *testing.T) {
	srv := fakeOllamaServer(t, struct {
		version    string
		tags       string
		generateOK bool
		chatOK     bool
	}{version: "0.32.1", tags: realisticTagsResponse, generateOK: true, chatOK: true})
	defer srv.Close()

	report := diagnoseOllama(context.Background(), ollamaDiagnosticsParams{
		BaseURL:       srv.URL,
		SelectedModel: "llama3.2:3b",
		DeepCheck:     true,
	})

	names := map[string]diagnosticSeverity{}
	for _, c := range report.Checks {
		names[c.Name] = c.Status
	}
	for _, want := range []string{"Generate endpoint works", "Chat endpoint works", "Streaming works"} {
		if status, ok := names[want]; !ok {
			t.Errorf("expected a %q check to have run", want)
		} else if status != severityOK {
			t.Errorf("%q status = %q, want ok", want, status)
		}
	}
}

func TestDiagnoseOllamaDeepCheckSurfacesGenerateFailure(t *testing.T) {
	srv := fakeOllamaServer(t, struct {
		version    string
		tags       string
		generateOK bool
		chatOK     bool
	}{version: "0.32.1", tags: realisticTagsResponse, generateOK: false, chatOK: true})
	defer srv.Close()

	report := diagnoseOllama(context.Background(), ollamaDiagnosticsParams{
		BaseURL:       srv.URL,
		SelectedModel: "llama3.2:3b",
		DeepCheck:     true,
	})

	var found bool
	for _, c := range report.Checks {
		if c.Name == "Generate endpoint works" {
			found = true
			if c.Status != severityError {
				t.Errorf("Generate endpoint works status = %q, want error", c.Status)
			}
		}
	}
	if !found {
		t.Fatal("expected a Generate endpoint works check")
	}
	if report.Status != severityError {
		t.Errorf("overall Status = %q, want error", report.Status)
	}
}

func TestDiagnoseOllamaTagsUnreachableFailsClosed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/version" {
			fmt.Fprint(w, `{"version":"0.32.1"}`)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	report := diagnoseOllama(context.Background(), ollamaDiagnosticsParams{BaseURL: srv.URL})
	if report.Status != severityError {
		t.Fatalf("Status = %q, want error when /api/tags fails even though /api/version succeeded", report.Status)
	}
}
