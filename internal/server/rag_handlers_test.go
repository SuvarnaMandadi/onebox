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

type fakeLLMClient struct {
	lastSystem, lastUser string
}

func (f *fakeLLMClient) Complete(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	f.lastSystem, f.lastUser = systemPrompt, userPrompt
	return "fake answer", nil
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

	srv := New(config.Config{JWTSecret: "test-secret", FilesDir: t.TempDir(), MaxUploadSize: 1 << 20}, sqlDB)
	srv.embeddingProvider = newFakeEmbeddingProvider()
	llmClient := &fakeLLMClient{}
	srv.llmClient = llmClient
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
