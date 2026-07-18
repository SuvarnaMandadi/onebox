package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"onebox/internal/config"
	"onebox/internal/db"
	"onebox/internal/llm"
)

// fakeEmbeddingProvider embeds text as a bag-of-words vector over a fixed
// vocabulary, so cosine similarity in tests reflects real word overlap
// instead of being random — enough to test ranking behavior without
// calling a real embeddings API.
type fakeEmbeddingProvider struct {
	vocab []string
}

func newFakeEmbeddingProvider() *fakeEmbeddingProvider {
	return &fakeEmbeddingProvider{vocab: []string{"cat", "dog", "sqlite", "onebox", "vector", "rag", "golang", "banana"}}
}

func (f *fakeEmbeddingProvider) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		lower := strings.ToLower(t)
		vec := make([]float32, len(f.vocab))
		for j, w := range f.vocab {
			vec[j] = float32(strings.Count(lower, w))
		}
		out[i] = vec
	}
	return out, nil
}

// fakeLLMClient implements llm.Provider without calling a real API. It's
// installed as the router's Anthropic backend, since RAG's answer
// endpoint defaults to cfg.AnthropicModel (routes to "anthropic").
type fakeLLMClient struct {
	lastSystem, lastUser string
}

func (f *fakeLLMClient) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResult, error) {
	for _, m := range req.Messages {
		switch m.Role {
		case "system":
			f.lastSystem = m.Content
		case "user":
			f.lastUser = m.Content
		}
	}
	return llm.ChatResult{Content: "fake answer", TokensIn: 10, TokensOut: 5}, nil
}

func (f *fakeLLMClient) ChatStream(ctx context.Context, req llm.ChatRequest, onDelta func(string)) (llm.ChatResult, error) {
	result, err := f.Chat(ctx, req)
	if err == nil {
		onDelta(result.Content)
	}
	return result, err
}

// newRAGTestServer is like newTestServer but wires in fake embedding/LLM
// clients so RAG tests don't need real API keys or network access.
func newRAGTestServer(t *testing.T) (*Server, *fakeLLMClient) {
	t.Helper()
	sqlDB, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.Migrate(sqlDB); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })

	srv := New(config.Config{JWTSecret: "test-secret", FilesDir: t.TempDir(), MaxUploadSize: 1 << 20, AnthropicModel: "claude-sonnet-5"}, sqlDB)
	llmClient := &fakeLLMClient{}
	srv.providers.Store(&providerBundle{
		embedding: newFakeEmbeddingProvider(),
		llm:       &llm.Router{Anthropic: llmClient},
	})
	return srv, llmClient
}

func uploadRAGSource(t *testing.T, srv *Server, token, filename string, content []byte) map[string]any {
	t.Helper()
	req := multipartUploadRequest(t, "/api/rag/sources", "file", filename, content)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("upload rag source failed: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var src map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &src); err != nil {
		t.Fatalf("decode rag source: %v", err)
	}
	return src
}

func waitForRAGSourceDone(t *testing.T, srv *Server, token, id string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		rec := doAuth(t, srv, http.MethodGet, "/api/rag/sources/"+id, token, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("get rag source failed: status = %d, body = %s", rec.Code, rec.Body.String())
		}
		var src map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &src); err != nil {
			t.Fatalf("decode rag source: %v", err)
		}
		switch src["status"] {
		case "done":
			return src
		case "error":
			t.Fatalf("ingestion errored: %v", src["error"])
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for rag source to finish ingesting")
	return nil
}

func TestRAGIngestAndQuery(t *testing.T) {
	srv, _ := newRAGTestServer(t)
	_, token := signupUser(t, srv, "researcher@example.com")

	src := uploadRAGSource(t, srv, token, "notes.txt", []byte(
		"onebox is a sqlite backed backend. It supports vector search for RAG. Cats and dogs are unrelated to golang.",
	))
	waitForRAGSourceDone(t, srv, token, src["id"].(string))

	rec := doAuth(t, srv, http.MethodPost, "/api/rag/query", token, ragQueryRequest{Query: "tell me about onebox and sqlite", TopK: 3})
	if rec.Code != http.StatusOK {
		t.Fatalf("query failed: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Results []ragScoredChunk `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode query response: %v", err)
	}
	if len(resp.Results) == 0 {
		t.Fatal("expected at least one result")
	}
	if resp.Results[0].Score <= 0 {
		t.Fatalf("top result score = %v, want > 0 given keyword overlap", resp.Results[0].Score)
	}
}

func TestRAGUnsupportedFileType(t *testing.T) {
	srv, _ := newRAGTestServer(t)
	_, token := signupUser(t, srv, "researcher@example.com")

	req := multipartUploadRequest(t, "/api/rag/sources", "file", "malware.exe", []byte("binary content"))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
	}
}

func TestListRAGSources(t *testing.T) {
	srv, _ := newRAGTestServer(t)
	_, userToken := signupUser(t, srv, "researcher@example.com")
	adminToken := bootstrapAdmin(t, srv)

	uploadRAGSource(t, srv, userToken, "a.txt", []byte("content a"))
	uploadRAGSource(t, srv, userToken, "b.txt", []byte("content b"))

	t.Run("admin can list", func(t *testing.T) {
		rec := doAuth(t, srv, http.MethodGet, "/api/rag/sources", adminToken, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
		}
		var resp struct {
			Items []map[string]any `json:"items"`
		}
		json.Unmarshal(rec.Body.Bytes(), &resp)
		if len(resp.Items) != 2 {
			t.Fatalf("got %d items, want 2", len(resp.Items))
		}
	})

	t.Run("non-admin rejected", func(t *testing.T) {
		rec := doAuth(t, srv, http.MethodGet, "/api/rag/sources", userToken, nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
	})
}

func TestRAGSourceOwnership(t *testing.T) {
	srv, _ := newRAGTestServer(t)
	_, ownerToken := signupUser(t, srv, "owner@example.com")
	_, otherToken := signupUser(t, srv, "other@example.com")

	src := uploadRAGSource(t, srv, ownerToken, "notes.txt", []byte("some content about onebox"))
	id := src["id"].(string)

	t.Run("owner can view", func(t *testing.T) {
		rec := doAuth(t, srv, http.MethodGet, "/api/rag/sources/"+id, ownerToken, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
	})

	t.Run("non-owner cannot view", func(t *testing.T) {
		rec := doAuth(t, srv, http.MethodGet, "/api/rag/sources/"+id, otherToken, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})

	t.Run("non-owner cannot delete", func(t *testing.T) {
		rec := doAuth(t, srv, http.MethodDelete, "/api/rag/sources/"+id, otherToken, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})

	t.Run("query only sees own chunks", func(t *testing.T) {
		waitForRAGSourceDone(t, srv, ownerToken, id)
		rec := doAuth(t, srv, http.MethodPost, "/api/rag/query", otherToken, ragQueryRequest{Query: "onebox", TopK: 5})
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
		}
		var resp struct {
			Results []ragScoredChunk `json:"results"`
		}
		json.Unmarshal(rec.Body.Bytes(), &resp)
		if len(resp.Results) != 0 {
			t.Fatalf("expected no results for a user with no ingested documents, got %d", len(resp.Results))
		}
	})

	t.Run("owner can delete", func(t *testing.T) {
		rec := doAuth(t, srv, http.MethodDelete, "/api/rag/sources/"+id, ownerToken, nil)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204, body = %s", rec.Code, rec.Body.String())
		}
	})
}

func TestRAGAnswer(t *testing.T) {
	srv, fakeLLM := newRAGTestServer(t)
	_, token := signupUser(t, srv, "researcher@example.com")

	src := uploadRAGSource(t, srv, token, "notes.txt", []byte("onebox uses sqlite and supports rag with vector search over golang code."))
	waitForRAGSourceDone(t, srv, token, src["id"].(string))

	rec := doAuth(t, srv, http.MethodPost, "/api/rag/answer", token, ragQueryRequest{Query: "what database does onebox use?", TopK: 3})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	var resp ragAnswerResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode answer response: %v", err)
	}
	if resp.Answer != "fake answer" {
		t.Fatalf("answer = %q, want %q", resp.Answer, "fake answer")
	}
	if len(resp.Sources) == 0 {
		t.Fatal("expected at least one source chunk")
	}
	if !strings.Contains(fakeLLM.lastUser, "what database does onebox use?") {
		t.Fatalf("prompt sent to LLM missing the question: %q", fakeLLM.lastUser)
	}
}

// TestRAGAnswerLogsActualProvider guards against a real bug found in a
// pre-release dry run: handleRAGAnswer hardcoded "anthropic" as the usage
// log's provider regardless of which provider the configured model
// actually routed to (llm.ProviderKind picks by name prefix — a non-Claude
// AnthropicModel, e.g. an Ollama model name used for RAG answers, was
// still logged as "anthropic", corrupting the usage/spend dashboard).
func TestRAGAnswerLogsActualProvider(t *testing.T) {
	srv, _ := newRAGTestServer(t)
	srv.cfg.AnthropicModel = "llama3.2:1b" // not a "claude*" name -> routes to Ollama
	ollamaFake := &fakeLLMClient{}
	bundle := *srv.providers.Load()
	bundle.llm = &llm.Router{Ollama: ollamaFake}
	srv.providers.Store(&bundle)

	_, token := signupUser(t, srv, "researcher@example.com")
	src := uploadRAGSource(t, srv, token, "notes.txt", []byte("onebox uses sqlite for storage."))
	waitForRAGSourceDone(t, srv, token, src["id"].(string))

	rec := doAuth(t, srv, http.MethodPost, "/api/rag/answer", token, ragQueryRequest{Query: "what does onebox use for storage?", TopK: 3})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}

	usageRec := doAuth(t, srv, http.MethodGet, "/api/usage", token, nil)
	var usageResp struct {
		Items []usageRecord `json:"items"`
	}
	json.Unmarshal(usageRec.Body.Bytes(), &usageResp)
	if len(usageResp.Items) != 1 {
		t.Fatalf("got %d usage records, want 1", len(usageResp.Items))
	}
	if usageResp.Items[0].Provider != "ollama" {
		t.Fatalf("usage provider = %q, want %q (model %q should route to ollama, not be hardcoded to anthropic)",
			usageResp.Items[0].Provider, "ollama", srv.cfg.AnthropicModel)
	}
}

func TestRAGAnswerNoDocuments(t *testing.T) {
	srv, _ := newRAGTestServer(t)
	_, token := signupUser(t, srv, "researcher@example.com")

	rec := doAuth(t, srv, http.MethodPost, "/api/rag/answer", token, ragQueryRequest{Query: "anything", TopK: 3})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	var resp ragAnswerResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Sources) != 0 {
		t.Fatalf("expected no sources, got %d", len(resp.Sources))
	}
}
