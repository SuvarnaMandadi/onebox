package server

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"onebox/internal/config"
	"onebox/internal/db"
	"onebox/internal/llm"
)

// countingProvider implements llm.Provider and counts calls, so tests can
// assert the cache actually prevented a second provider call.
type countingProvider struct {
	calls  int
	result llm.ChatResult
}

func (p *countingProvider) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResult, error) {
	p.calls++
	return p.result, nil
}

func (p *countingProvider) ChatStream(ctx context.Context, req llm.ChatRequest, onDelta func(string)) (llm.ChatResult, error) {
	p.calls++
	onDelta(p.result.Content)
	return p.result, nil
}

func newLLMTestServer(t *testing.T, cfg config.Config) (*Server, *countingProvider) {
	t.Helper()
	sqlDB, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.Migrate(sqlDB); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })

	if cfg.JWTSecret == "" {
		cfg.JWTSecret = "test-secret"
	}
	srv := New(cfg, sqlDB)
	provider := &countingProvider{result: llm.ChatResult{Content: "hello from fake", TokensIn: 100, TokensOut: 50}}
	bundle := *srv.providers.Load()
	bundle.llm = &llm.Router{Anthropic: provider}
	srv.providers.Store(&bundle)
	return srv, provider
}

func TestLLMChatBasic(t *testing.T) {
	srv, provider := newLLMTestServer(t, config.Config{})
	_, token := signupUser(t, srv, "chatter@example.com")

	rec := doAuth(t, srv, http.MethodPost, "/api/llm/chat", token, llmChatRequest{
		Model:    "claude-sonnet-5",
		Messages: []llm.Message{{Role: "user", Content: "hi"}},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	var resp llmChatResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Content != "hello from fake" || resp.Cached {
		t.Fatalf("resp = %+v, want uncached fake content", resp)
	}
	if provider.calls != 1 {
		t.Fatalf("provider.calls = %d, want 1", provider.calls)
	}
}

func TestLLMChatCaching(t *testing.T) {
	srv, provider := newLLMTestServer(t, config.Config{})
	_, token := signupUser(t, srv, "chatter@example.com")

	req := llmChatRequest{Model: "claude-sonnet-5", Messages: []llm.Message{{Role: "user", Content: "same question"}}}

	first := doAuth(t, srv, http.MethodPost, "/api/llm/chat", token, req)
	if first.Code != http.StatusOK {
		t.Fatalf("first status = %d, body = %s", first.Code, first.Body.String())
	}
	var firstResp llmChatResponse
	json.Unmarshal(first.Body.Bytes(), &firstResp)
	if firstResp.Cached {
		t.Fatal("first request should not be cached")
	}

	second := doAuth(t, srv, http.MethodPost, "/api/llm/chat", token, req)
	if second.Code != http.StatusOK {
		t.Fatalf("second status = %d, body = %s", second.Code, second.Body.String())
	}
	var secondResp llmChatResponse
	json.Unmarshal(second.Body.Bytes(), &secondResp)
	if !secondResp.Cached {
		t.Fatal("second identical request should be served from cache")
	}
	if provider.calls != 1 {
		t.Fatalf("provider.calls = %d, want 1 (second request should hit cache, not the provider)", provider.calls)
	}
}

func TestLLMChatRateLimit(t *testing.T) {
	srv, _ := newLLMTestServer(t, config.Config{RateLimitPerMinute: 1})
	_, token := signupUser(t, srv, "chatter@example.com")

	req := llmChatRequest{Model: "claude-sonnet-5", Messages: []llm.Message{{Role: "user", Content: "one"}}}
	first := doAuth(t, srv, http.MethodPost, "/api/llm/chat", token, req)
	if first.Code != http.StatusOK {
		t.Fatalf("first status = %d, want 200, body = %s", first.Code, first.Body.String())
	}

	req2 := llmChatRequest{Model: "claude-sonnet-5", Messages: []llm.Message{{Role: "user", Content: "two"}}}
	second := doAuth(t, srv, http.MethodPost, "/api/llm/chat", token, req2)
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("second status = %d, want 429, body = %s", second.Code, second.Body.String())
	}
}

func TestLLMChatSpendCap(t *testing.T) {
	// 100 tokens in + 50 out at claude-sonnet-5 pricing ($3/$15 per 1M)
	// costs (100*3 + 50*15)/1e6 = $0.00105 per call; a cap below that
	// blocks the very next call.
	srv, _ := newLLMTestServer(t, config.Config{MonthlySpendCapUSD: 0.0005})
	_, token := signupUser(t, srv, "chatter@example.com")

	req := llmChatRequest{Model: "claude-sonnet-5", Messages: []llm.Message{{Role: "user", Content: "one"}}}
	first := doAuth(t, srv, http.MethodPost, "/api/llm/chat", token, req)
	if first.Code != http.StatusOK {
		t.Fatalf("first status = %d, want 200, body = %s", first.Code, first.Body.String())
	}

	req2 := llmChatRequest{Model: "claude-sonnet-5", Messages: []llm.Message{{Role: "user", Content: "two"}}}
	second := doAuth(t, srv, http.MethodPost, "/api/llm/chat", token, req2)
	if second.Code != http.StatusPaymentRequired {
		t.Fatalf("second status = %d, want 402, body = %s", second.Code, second.Body.String())
	}
}

func TestLLMChatUnconfiguredProvider(t *testing.T) {
	srv, _ := newLLMTestServer(t, config.Config{})
	_, token := signupUser(t, srv, "chatter@example.com")

	rec := doAuth(t, srv, http.MethodPost, "/api/llm/chat", token, llmChatRequest{
		Model:    "gpt-4o",
		Messages: []llm.Message{{Role: "user", Content: "hi"}},
	})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "OpenAI") {
		t.Fatalf("expected error to mention OpenAI, got %s", rec.Body.String())
	}
}

func TestLLMChatStream(t *testing.T) {
	srv, provider := newLLMTestServer(t, config.Config{})
	provider.result = llm.ChatResult{Content: "streamed answer", TokensIn: 20, TokensOut: 10}
	_, token := signupUser(t, srv, "chatter@example.com")

	httpSrv := httptest.NewServer(srv.Router())
	defer httpSrv.Close()

	body := `{"model":"claude-sonnet-5","messages":[{"role":"user","content":"hi"}],"stream":true}`
	req, _ := http.NewRequest(http.MethodPost, httpSrv.URL+"/api/llm/chat", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	var gotDelta bool
	var gotDone bool
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			gotDone = true
			break
		}
		var msg map[string]string
		if err := json.Unmarshal([]byte(payload), &msg); err == nil && msg["delta"] == "streamed answer" {
			gotDelta = true
		}
	}
	if !gotDelta {
		t.Fatal("did not receive the expected delta event")
	}
	if !gotDone {
		t.Fatal("did not receive [DONE]")
	}

	// Usage should have been logged for the streamed call too.
	usageRec := doAuth(t, srv, http.MethodGet, "/api/usage", token, nil)
	var usageResp struct {
		Items []usageRecord `json:"items"`
	}
	json.Unmarshal(usageRec.Body.Bytes(), &usageResp)
	if len(usageResp.Items) != 1 {
		t.Fatalf("got %d usage records, want 1", len(usageResp.Items))
	}
}

func TestUsageEndpointScoping(t *testing.T) {
	srv, _ := newLLMTestServer(t, config.Config{})
	_, tokenA := signupUser(t, srv, "a@example.com")
	_, tokenB := signupUser(t, srv, "b@example.com")
	adminToken := bootstrapAdmin(t, srv)

	req := llmChatRequest{Model: "claude-sonnet-5", Messages: []llm.Message{{Role: "user", Content: "hi"}}}
	doAuth(t, srv, http.MethodPost, "/api/llm/chat", tokenA, req)
	doAuth(t, srv, http.MethodPost, "/api/llm/chat", tokenB, req)

	t.Run("user sees only own usage", func(t *testing.T) {
		rec := doAuth(t, srv, http.MethodGet, "/api/usage", tokenA, nil)
		var resp struct {
			Items []usageRecord `json:"items"`
		}
		json.Unmarshal(rec.Body.Bytes(), &resp)
		if len(resp.Items) != 1 {
			t.Fatalf("got %d items, want 1", len(resp.Items))
		}
	})

	t.Run("admin sees all usage", func(t *testing.T) {
		rec := doAuth(t, srv, http.MethodGet, "/api/usage", adminToken, nil)
		var resp struct {
			Items []usageRecord `json:"items"`
		}
		json.Unmarshal(rec.Body.Bytes(), &resp)
		if len(resp.Items) != 2 {
			t.Fatalf("got %d items, want 2", len(resp.Items))
		}
	})
}
