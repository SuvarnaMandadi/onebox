package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOllamaClientChat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("path = %q, want /api/chat", r.URL.Path)
		}
		var req ollamaChatRequest
		json.NewDecoder(r.Body).Decode(&req)
		if req.Stream {
			t.Error("expected stream=false for non-streaming Chat()")
		}
		json.NewEncoder(w).Encode(ollamaChatChunk{
			Message:         ollamaChatMessageWire{Content: "hi from llama"},
			Done:            true,
			PromptEvalCount: 6,
			EvalCount:       4,
		})
	}))
	defer srv.Close()

	c := NewOllamaClient(srv.URL)
	result, err := c.Chat(context.Background(), ChatRequest{Model: "llama3", Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if result.Content != "hi from llama" {
		t.Fatalf("content = %q, want %q", result.Content, "hi from llama")
	}
	if result.TokensIn != 6 || result.TokensOut != 4 {
		t.Fatalf("tokens = (%d, %d), want (6, 4)", result.TokensIn, result.TokensOut)
	}
}

func TestOllamaClientChatStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chunks := []ollamaChatChunk{
			{Message: ollamaChatMessageWire{Content: "Hi"}},
			{Message: ollamaChatMessageWire{Content: " there"}},
			{Done: true, PromptEvalCount: 5, EvalCount: 2},
		}
		for _, c := range chunks {
			b, _ := json.Marshal(c)
			w.Write(append(b, '\n'))
		}
	}))
	defer srv.Close()

	c := NewOllamaClient(srv.URL)
	var deltas []string
	result, err := c.ChatStream(context.Background(), ChatRequest{Model: "llama3", Messages: []Message{{Role: "user", Content: "hi"}}}, func(d string) {
		deltas = append(deltas, d)
	})
	if err != nil {
		t.Fatalf("ChatStream() error = %v", err)
	}
	if result.Content != "Hi there" {
		t.Fatalf("content = %q, want %q", result.Content, "Hi there")
	}
	if strings.Join(deltas, "") != "Hi there" {
		t.Fatalf("deltas joined = %q, want %q", strings.Join(deltas, ""), "Hi there")
	}
	if result.TokensIn != 5 || result.TokensOut != 2 {
		t.Fatalf("tokens = (%d, %d), want (5, 2)", result.TokensIn, result.TokensOut)
	}
}

func TestOllamaClientListModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Errorf("path = %q, want /api/tags", r.URL.Path)
		}
		json.NewEncoder(w).Encode(ollamaTagsResponse{
			Models: []struct {
				Name string `json:"name"`
			}{{Name: "llama3.2:3b"}, {Name: "qwen"}, {Name: "mistral"}},
		})
	}))
	defer srv.Close()

	c := NewOllamaClient(srv.URL)
	models, err := c.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	want := []string{"llama3.2:3b", "qwen", "mistral"}
	if len(models) != len(want) {
		t.Fatalf("models = %v, want %v", models, want)
	}
	for i, m := range want {
		if models[i] != m {
			t.Fatalf("models[%d] = %q, want %q", i, models[i], m)
		}
	}
}

func TestOllamaClientListModelsUnreachable(t *testing.T) {
	c := NewOllamaClient("http://127.0.0.1:1") // nothing listens here
	if _, err := c.ListModels(context.Background()); err == nil {
		t.Fatal("ListModels() against an unreachable daemon: expected error, got nil")
	}
}

// TestIsEmbeddingModel pins down the heuristic behind the fix for "the
// Chat Provider model selector is populated with the embedding model" —
// every example the bug report and the Embedding Provider's own docs give
// (nomic-embed-text, bge, e5, voyage) must be recognized, alongside the
// other common embedding families, while ordinary chat models never
// false-positive.
func TestIsEmbeddingModel(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"nomic-embed-text", true},
		{"nomic-embed-text:latest", true},
		{"bge-large", true},
		{"bge-m3:latest", true},
		{"e5-large-v2", true},
		{"intfloat-e5", true},
		{"gte-base", true},
		{"all-minilm", true},
		{"voyage-2", true},
		{"llama3.2:3b", false},
		{"llama3.2:1b", false},
		{"qwen", false},
		{"mistral", false},
		{"gemma2:9b", false},
	}
	for _, c := range cases {
		if got := IsEmbeddingModel(c.name); got != c.want {
			t.Errorf("IsEmbeddingModel(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestOllamaClientListChatModelsFiltersEmbeddingOnly is the "Ollama chat
// model discovery" + "filtering embedding models" regression test at the
// client layer: a daemon with a mix of chat and embedding models installed
// must only offer the chat ones.
func TestOllamaClientListChatModelsFiltersEmbeddingOnly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(ollamaTagsResponse{
			Models: []struct {
				Name string `json:"name"`
			}{{Name: "llama3.2:3b"}, {Name: "nomic-embed-text"}, {Name: "qwen"}, {Name: "bge-large"}},
		})
	}))
	defer srv.Close()

	c := NewOllamaClient(srv.URL)
	models, err := c.ListChatModels(context.Background())
	if err != nil {
		t.Fatalf("ListChatModels() error = %v", err)
	}
	want := []string{"llama3.2:3b", "qwen"}
	if len(models) != len(want) {
		t.Fatalf("models = %v, want %v", models, want)
	}
	for i, m := range want {
		if models[i] != m {
			t.Fatalf("models[%d] = %q, want %q", i, models[i], m)
		}
	}
}

// TestOllamaClientListChatModelsAllEmbeddingOnly covers the exact scenario
// reported: a daemon that only has an embedding model installed (e.g.
// someone who set up Ollama purely for RAG embeddings, per Turn 4) must
// report zero chat models rather than falling back to offering the
// embedding model anyway.
func TestOllamaClientListChatModelsAllEmbeddingOnly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(ollamaTagsResponse{
			Models: []struct {
				Name string `json:"name"`
			}{{Name: "nomic-embed-text"}},
		})
	}))
	defer srv.Close()

	c := NewOllamaClient(srv.URL)
	models, err := c.ListChatModels(context.Background())
	if err != nil {
		t.Fatalf("ListChatModels() error = %v", err)
	}
	if len(models) != 0 {
		t.Fatalf("models = %v, want empty — a daemon with only an embedding model installed has zero chat models", models)
	}
}

func TestOllamaClientVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/version" {
			t.Errorf("path = %q, want /api/version", r.URL.Path)
		}
		json.NewEncoder(w).Encode(ollamaVersionResponse{Version: "0.5.4"})
	}))
	defer srv.Close()

	c := NewOllamaClient(srv.URL)
	version, err := c.Version(context.Background())
	if err != nil {
		t.Fatalf("Version() error = %v", err)
	}
	if version != "0.5.4" {
		t.Fatalf("version = %q, want %q", version, "0.5.4")
	}
}
