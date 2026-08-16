package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
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

// fakeLLMClient implements llm.Provider without calling a real API. Tests
// install it as whichever router backend (Anthropic, Ollama, ...) matches
// the providerBundle.chat.Provider they configure — see newRAGTestServer,
// which wires it in as Anthropic by default.
type fakeLLMClient struct {
	lastSystem, lastUser string
	// lastMessages is the full Messages slice from the most recent call —
	// lastSystem/lastUser above only capture the last message of each
	// role, which loses ordering/multiplicity; tests that care about
	// conversation history (multiple user/assistant turns in one request)
	// read this instead.
	lastMessages []llm.Message
	// lastTools is the Tools slice from the most recent request — tests
	// assert on this to confirm actionToolDefs was (or wasn't) offered on
	// a given code path (see TestFastPathOffersNoTools,
	// TestChatbotAutoExecutesSafeActionNonStreaming in
	// chatbot_actions_test.go).
	lastTools []llm.Tool
	// reply overrides the fixed "fake answer" content when set — lets
	// action-pipeline tests assert Reply and Actions are independent of
	// each other. Used on every call unless roundReplies (below) is set,
	// in which case it's only the fallback for a call past the end of that
	// slice.
	reply string
	// toolCalls is returned as ChatResult.ToolCalls verbatim on every call
	// — lets tests simulate a model that decided to call one of
	// actionToolDefs. Only correct for a single-round scenario (the call
	// never gets auto-executed, so the tool-execution loop — see
	// runToolLoop in chatbot_tool_execution.go — never issues a second
	// round); for a scenario where the model's tool call DOES get
	// auto-executed, use roundToolCalls instead, or this fixed field would
	// make every subsequent round see the exact same call again.
	toolCalls []llm.ToolCall
	// roundReplies/roundToolCalls, when set, provide per-call (round-
	// indexed, first call = index 0) Content/ToolCalls instead of the
	// fixed reply/toolCalls above — needed for any test where a tool call
	// actually gets executed (the loop then issues a real second round,
	// which must see different canned output or the fake would just
	// repeat the same tool call forever, hitting the loop's round cap
	// instead of resolving). A call past the end of roundToolCalls gets no
	// tool calls (nil) — the natural "the model is done" signal.
	roundReplies   []string
	roundToolCalls [][]llm.ToolCall
	// callCount is incremented on every Chat() call — read by tests that
	// want to assert exactly how many round-trips the tool-execution loop
	// made.
	callCount int
}

func (f *fakeLLMClient) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResult, error) {
	round := f.callCount
	f.callCount++
	f.lastMessages = req.Messages
	f.lastTools = req.Tools
	for _, m := range req.Messages {
		switch m.Role {
		case "system":
			f.lastSystem = m.Content
		case "user":
			f.lastUser = m.Content
		}
	}
	content := f.reply
	if round < len(f.roundReplies) {
		content = f.roundReplies[round]
	}
	if content == "" {
		content = "fake answer"
	}
	calls := f.toolCalls
	if f.roundToolCalls != nil {
		if round < len(f.roundToolCalls) {
			calls = f.roundToolCalls[round]
		} else {
			calls = nil
		}
	}
	return llm.ChatResult{Content: content, ToolCalls: calls, TokensIn: 10, TokensOut: 5}, nil
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

	srv := New(config.Config{JWTSecret: "test-secret", FilesDir: t.TempDir(), MaxUploadSize: 1 << 20}, sqlDB)
	llmClient := &fakeLLMClient{}
	srv.providers.Store(&providerBundle{
		embedding: newFakeEmbeddingProvider(),
		llm:       &llm.Router{Anthropic: llmClient},
		chat:      chatSelection{Provider: "anthropic", Model: "claude-sonnet-5"},
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

// TestRAGIngestDOCX uploads a real .docx (headings, paragraphs, and a
// table — see internal/server/testdata/sample.docx) through the actual
// /api/rag/sources endpoint and confirms the extracted table content
// made it all the way through extraction, chunking, and storage.
func TestRAGIngestDOCX(t *testing.T) {
	srv, _ := newRAGTestServer(t)
	_, token := signupUser(t, srv, "researcher@example.com")

	content, err := os.ReadFile("testdata/sample.docx")
	if err != nil {
		t.Fatalf("read testdata/sample.docx: %v", err)
	}

	src := uploadRAGSource(t, srv, token, "sample.docx", content)
	done := waitForRAGSourceDone(t, srv, token, src["id"].(string))
	if done["chunk_count"].(float64) < 1 {
		t.Fatalf("chunk_count = %v, want >= 1", done["chunk_count"])
	}

	rec := doAuth(t, srv, http.MethodPost, "/api/rag/query", token, ragQueryRequest{Query: "pricing table", TopK: 5})
	if rec.Code != http.StatusOK {
		t.Fatalf("query failed: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Results []ragScoredChunk `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode query response: %v", err)
	}

	var allText string
	for _, r := range resp.Results {
		allText += r.Text
	}
	for _, want := range []string{"Refund Policy", "Monthly Price", "Starter", "$19", "Pro", "$49"} {
		if !strings.Contains(allText, want) {
			t.Errorf("ingested/retrieved text missing %q; got chunks: %q", want, allText)
		}
	}
}

// TestRAGUnsupportedFileType checks not just that an unsupported upload is
// rejected, but that the rejection is a clear, actionable error — never a
// silent failure — naming every format that IS supported.
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

	var env errorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if env.Code != "unsupported_type" {
		t.Fatalf("code = %q, want %q", env.Code, "unsupported_type")
	}
	if !strings.Contains(env.Message, ".exe") {
		t.Errorf("message should name the rejected extension, got: %q", env.Message)
	}
	for _, ext := range supportedRAGExtensionsList {
		if !strings.Contains(env.Message, ext) {
			t.Errorf("message should list supported extension %q, got: %q", ext, env.Message)
		}
	}
	if env.Details == nil {
		t.Error("expected structured details (received/supported) alongside the message, got nil")
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

	t.Run("non-admin sees only own sources", func(t *testing.T) {
		rec := doAuth(t, srv, http.MethodGet, "/api/rag/sources", userToken, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
		}
		var resp struct {
			Items []map[string]any `json:"items"`
			Total int              `json:"total"`
		}
		json.Unmarshal(rec.Body.Bytes(), &resp)
		if len(resp.Items) != 2 || resp.Total != 2 {
			t.Fatalf("got %d items (total=%d), want 2 (both owned by this user)", len(resp.Items), resp.Total)
		}
	})

	t.Run("unauthenticated rejected", func(t *testing.T) {
		rec := doAuth(t, srv, http.MethodGet, "/api/rag/sources", "", nil)
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

// TestRAGAnswerUsesConfiguredChatProvider is the RAG-answer half of "switching
// Chat Provider changes the backend used": handleRAGAnswer must call
// whichever provider bundle.chat.Provider names — via llm.Router.Named/
// ChatWithProvider, not model-name prefix guessing (llm.ProviderKind) — and
// log usage under that same provider. Before the Chat Provider refactor,
// /api/rag/answer was hardcoded to cfg.AnthropicModel regardless of what
// was actually configured; this pins the replacement behavior down at the
// HTTP layer, complementing the llm-package-level
// TestRouterChatWithProviderIgnoresModelPrefix.
func TestRAGAnswerUsesConfiguredChatProvider(t *testing.T) {
	srv, anthropicFake := newRAGTestServer(t)
	ollamaFake := &fakeLLMClient{}
	bundle := *srv.providers.Load()
	bundle.llm = &llm.Router{Anthropic: anthropicFake, Ollama: ollamaFake}
	bundle.chat = chatSelection{Provider: "ollama", Model: "llama3.2:1b"}
	srv.providers.Store(&bundle)

	_, token := signupUser(t, srv, "researcher@example.com")
	src := uploadRAGSource(t, srv, token, "notes.txt", []byte("onebox uses sqlite for storage."))
	waitForRAGSourceDone(t, srv, token, src["id"].(string))

	rec := doAuth(t, srv, http.MethodPost, "/api/rag/answer", token, ragQueryRequest{Query: "what does onebox use for storage?", TopK: 3})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}

	if ollamaFake.lastUser == "" {
		t.Fatal("expected the Ollama fake to have received the request — chat_provider=ollama should route there")
	}
	if anthropicFake.lastUser != "" {
		t.Fatal("Anthropic fake received a request even though chat_provider was set to ollama")
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
		t.Fatalf("usage provider = %q, want %q (chat_provider=ollama must be logged accurately, not hardcoded)",
			usageResp.Items[0].Provider, "ollama")
	}
}

// TestRAGAnswerNoChatModelConfigured guards the other new failure mode:
// with no chat model chosen (the default state — chat_provider defaults
// to "ollama" with no model until an operator picks one in Settings),
// /api/rag/answer must fail with a clear, actionable error instead of
// either silently calling Anthropic or panicking on an empty model string.
func TestRAGAnswerNoChatModelConfigured(t *testing.T) {
	srv, _ := newRAGTestServer(t)
	bundle := *srv.providers.Load()
	bundle.chat = chatSelection{Provider: "ollama", Model: ""}
	srv.providers.Store(&bundle)

	_, token := signupUser(t, srv, "researcher@example.com")
	src := uploadRAGSource(t, srv, token, "notes.txt", []byte("onebox uses sqlite for storage."))
	waitForRAGSourceDone(t, srv, token, src["id"].(string))

	rec := doAuth(t, srv, http.MethodPost, "/api/rag/answer", token, ragQueryRequest{Query: "what does onebox use for storage?", TopK: 3})
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Settings") {
		t.Fatalf("expected an actionable error pointing at Settings, got %s", rec.Body.String())
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
