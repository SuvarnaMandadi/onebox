package llm

import (
	"context"
	"encoding/json"
	"fmt"
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
			Models: []ollamaTagModel{{Name: "llama3.2:3b"}, {Name: "qwen"}, {Name: "mistral"}},
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
			Models: []ollamaTagModel{{Name: "llama3.2:3b"}, {Name: "nomic-embed-text"}, {Name: "qwen"}, {Name: "bge-large"}},
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
			Models: []ollamaTagModel{{Name: "nomic-embed-text"}},
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

func TestOllamaClientListModelsDetailedParsesFullShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Errorf("path = %q, want /api/tags", r.URL.Path)
		}
		// Mirrors a real modern (0.5+) Ollama daemon's response shape —
		// see diagnostics_ollama.go's doc comment for where this was
		// verified against a live instance.
		fmt.Fprint(w, `{"models":[
			{"name":"llama3.2:3b","size":2019393189,"details":{"family":"llama","families":["llama"],"parameter_size":"3.2B","quantization_level":"Q4_K_M","context_length":131072,"embedding_length":3072},"capabilities":["completion","tools"]},
			{"name":"nomic-embed-text:latest","size":274302450,"details":{"family":"nomic-bert","families":["nomic-bert"],"parameter_size":"137M","quantization_level":"F16","context_length":2048,"embedding_length":768},"capabilities":["embedding"]}
		]}`)
	}))
	defer srv.Close()

	c := NewOllamaClient(srv.URL)
	models, err := c.ListModelsDetailed(context.Background())
	if err != nil {
		t.Fatalf("ListModelsDetailed() error = %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("len(models) = %d, want 2", len(models))
	}
	chat := models[0]
	if chat.Name != "llama3.2:3b" || chat.SizeBytes != 2019393189 || chat.QuantizationLevel != "Q4_K_M" || chat.ParameterSize != "3.2B" || chat.ContextLength != 131072 {
		t.Fatalf("chat model detail = %+v, missing expected real fields", chat)
	}
	if !chat.CapabilitiesKnown {
		t.Fatal("CapabilitiesKnown = false, want true — this response includes a capabilities array")
	}
	if !containsStr(chat.Capabilities, "tools") {
		t.Fatalf("capabilities = %v, want to include \"tools\"", chat.Capabilities)
	}

	embed := models[1]
	if !containsStr(embed.Capabilities, "embedding") {
		t.Fatalf("embedding model capabilities = %v, want to include \"embedding\"", embed.Capabilities)
	}
}

func containsStr(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// TestOllamaClientListModelsDetailedOlderDaemon pins the graceful-
// degradation path: a daemon predating the capabilities/context_length
// fields (older than ~0.5) still parses cleanly, just with
// CapabilitiesKnown=false rather than silently claiming "zero
// capabilities" as if that were a verified fact.
func TestOllamaClientListModelsDetailedOlderDaemon(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"models":[{"name":"llama3.2:3b"}]}`)
	}))
	defer srv.Close()

	c := NewOllamaClient(srv.URL)
	models, err := c.ListModelsDetailed(context.Background())
	if err != nil {
		t.Fatalf("ListModelsDetailed() error = %v", err)
	}
	if len(models) != 1 {
		t.Fatalf("len(models) = %d, want 1", len(models))
	}
	if models[0].CapabilitiesKnown {
		t.Fatal("CapabilitiesKnown = true, want false — this response has no capabilities field at all")
	}
}

func TestOllamaClientGenerateSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/generate" {
			t.Errorf("path = %q, want /api/generate", r.URL.Path)
		}
		var req ollamaGenerateRequest
		json.NewDecoder(r.Body).Decode(&req)
		if req.Stream {
			t.Error("expected stream=false for Generate()")
		}
		if req.Options.NumPredict != 4 {
			t.Errorf("NumPredict = %d, want 4 (bounded)", req.Options.NumPredict)
		}
		json.NewEncoder(w).Encode(ollamaGenerateResponse{Response: "ok", Done: true})
	}))
	defer srv.Close()

	c := NewOllamaClient(srv.URL)
	out, err := c.Generate(context.Background(), "llama3.2:3b", "hi", 4)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if out != "ok" {
		t.Fatalf("Generate() = %q, want %q", out, "ok")
	}
}

func TestOllamaClientGenerateModelMissing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(ollamaGenerateResponse{Error: `model "ghost" not found`})
	}))
	defer srv.Close()

	c := NewOllamaClient(srv.URL)
	_, err := c.Generate(context.Background(), "ghost", "hi", 4)
	if err == nil {
		t.Fatal("expected an error for a missing model, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("error = %v, want it to mention the model wasn't found", err)
	}
}

func TestOllamaClientPullStreamsRealProgress(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/pull" {
			t.Errorf("path = %q, want /api/pull", r.URL.Path)
		}
		flusher := w.(http.Flusher)
		lines := []string{
			`{"status":"pulling manifest"}`,
			`{"status":"downloading","digest":"sha256:abc","total":1000,"completed":500}`,
			`{"status":"downloading","digest":"sha256:abc","total":1000,"completed":1000}`,
			`{"status":"success"}`,
		}
		for _, l := range lines {
			fmt.Fprintln(w, l)
			flusher.Flush()
		}
	}))
	defer srv.Close()

	c := NewOllamaClient(srv.URL)
	var progress []OllamaPullProgress
	err := c.Pull(context.Background(), "llama3.2:3b", func(p OllamaPullProgress) {
		progress = append(progress, p)
	})
	if err != nil {
		t.Fatalf("Pull() error = %v", err)
	}
	if len(progress) != 4 {
		t.Fatalf("got %d progress events, want 4", len(progress))
	}
	if progress[len(progress)-1].Status != "success" {
		t.Fatalf("final status = %q, want %q", progress[len(progress)-1].Status, "success")
	}
	if progress[1].Completed != 500 || progress[1].Total != 1000 {
		t.Fatalf("progress[1] = %+v, want real byte counts from the daemon", progress[1])
	}
}

func TestOllamaClientPullPropagatesError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, `{"error":"pull model manifest: file does not exist"}`)
	}))
	defer srv.Close()

	c := NewOllamaClient(srv.URL)
	err := c.Pull(context.Background(), "does-not-exist", func(OllamaPullProgress) {})
	if err == nil {
		t.Fatal("expected an error for a nonexistent model, got nil")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("error = %v, want it to include the daemon's own message", err)
	}
}
