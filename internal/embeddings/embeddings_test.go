package embeddings

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAIProviderEmbed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embeddings" {
			t.Errorf("path = %q, want /embeddings", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer test-key")
		}
		var req openAIEmbedRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if len(req.Input) != 2 {
			t.Fatalf("got %d inputs, want 2", len(req.Input))
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(openAIEmbedResponse{
			Data: []struct {
				Embedding []float32 `json:"embedding"`
				Index     int       `json:"index"`
			}{
				{Embedding: []float32{0.1, 0.2}, Index: 1},
				{Embedding: []float32{0.3, 0.4}, Index: 0},
			},
		})
	}))
	defer srv.Close()

	p := NewOpenAIProvider(srv.URL, "test-key", "text-embedding-3-small")
	vecs, err := p.Embed(context.Background(), []string{"hello", "world"})
	if err != nil {
		t.Fatalf("Embed() error = %v", err)
	}
	if len(vecs) != 2 {
		t.Fatalf("got %d vectors, want 2", len(vecs))
	}
	// index 0 belongs to "hello" and should come from the server's
	// Index:0 entry ({0.3, 0.4}), regardless of response ordering.
	if vecs[0][0] != 0.3 || vecs[1][0] != 0.1 {
		t.Fatalf("vectors not reordered by index: %+v", vecs)
	}
}

func TestOpenAIProviderEmbedAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"message": "invalid api key"}})
	}))
	defer srv.Close()

	p := NewOpenAIProvider(srv.URL, "bad-key", "text-embedding-3-small")
	_, err := p.Embed(context.Background(), []string{"hello"})
	if err == nil {
		t.Fatal("expected error for 401 response, got nil")
	}
}

func TestOllamaProviderEmbed(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/embeddings" {
			t.Errorf("path = %q, want /api/embeddings", r.URL.Path)
		}
		var req ollamaEmbedRequest
		json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ollamaEmbedResponse{Embedding: []float32{float32(len(req.Prompt)), 0}})
	}))
	defer srv.Close()

	p := NewOllamaProvider(srv.URL, "nomic-embed-text")
	vecs, err := p.Embed(context.Background(), []string{"a", "bb", "ccc"})
	if err != nil {
		t.Fatalf("Embed() error = %v", err)
	}
	if calls != 3 {
		t.Fatalf("made %d requests, want 3 (one per text)", calls)
	}
	if len(vecs) != 3 || vecs[0][0] != 1 || vecs[1][0] != 2 || vecs[2][0] != 3 {
		t.Fatalf("unexpected vectors: %+v", vecs)
	}
}
