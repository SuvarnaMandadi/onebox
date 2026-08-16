package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAnthropicClientChat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("path = %q, want /v1/messages", r.URL.Path)
		}
		if got := r.Header.Get("x-api-key"); got != "test-key" {
			t.Errorf("x-api-key = %q, want test-key", got)
		}
		var req anthropicRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.System != "be helpful" {
			t.Errorf("system = %q, want %q", req.System, "be helpful")
		}
		if len(req.Messages) != 1 || req.Messages[0].Content != "what is onebox?" {
			t.Fatalf("unexpected messages: %+v", req.Messages)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(anthropicResponse{
			Content: []anthropicResponseBlock{{Type: "text", Text: "an all-in-one AI backend"}},
			Usage:   anthropicUsage{InputTokens: 10, OutputTokens: 5},
		})
	}))
	defer srv.Close()

	c := NewAnthropicClient(srv.URL, "test-key")
	result, err := c.Chat(context.Background(), ChatRequest{
		Model:    "claude-sonnet-5",
		Messages: []Message{{Role: "system", Content: "be helpful"}, {Role: "user", Content: "what is onebox?"}},
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if result.Content != "an all-in-one AI backend" {
		t.Fatalf("content = %q, want %q", result.Content, "an all-in-one AI backend")
	}
	if result.TokensIn != 10 || result.TokensOut != 5 {
		t.Fatalf("tokens = (%d, %d), want (10, 5)", result.TokensIn, result.TokensOut)
	}
}

// TestAnthropicClientChatDetectsTruncation is the truncation-detection
// regression test (security/reliability audit Fix 9): neither the
// non-streaming nor the streaming response parsing used to read Anthropic's
// stop_reason field at all, so a reply cut off mid-generation by
// defaultMaxTokens (1024) was silently indistinguishable from a normal
// completion — a truncated tool_use block's JSON would just fail to
// unmarshal downstream with no clue why. Chat now surfaces
// stop_reason=="max_tokens" as ChatResult.Truncated.
func TestAnthropicClientChatDetectsTruncation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(anthropicResponse{
			Content:    []anthropicResponseBlock{{Type: "text", Text: "cut off mid-sent"}},
			Usage:      anthropicUsage{InputTokens: 10, OutputTokens: 1024},
			StopReason: "max_tokens",
		})
	}))
	defer srv.Close()

	c := NewAnthropicClient(srv.URL, "test-key")
	result, err := c.Chat(context.Background(), ChatRequest{Model: "claude-sonnet-5", Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if !result.Truncated {
		t.Fatal("expected Truncated = true when stop_reason is max_tokens")
	}
}

// TestAnthropicClientChatNormalCompletionNotTruncated is the control case:
// a normal stop_reason ("end_turn", "tool_use", ...) must never be flagged
// as truncated.
func TestAnthropicClientChatNormalCompletionNotTruncated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(anthropicResponse{
			Content:    []anthropicResponseBlock{{Type: "text", Text: "a complete answer"}},
			Usage:      anthropicUsage{InputTokens: 10, OutputTokens: 5},
			StopReason: "end_turn",
		})
	}))
	defer srv.Close()

	c := NewAnthropicClient(srv.URL, "test-key")
	result, err := c.Chat(context.Background(), ChatRequest{Model: "claude-sonnet-5", Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if result.Truncated {
		t.Fatal("expected Truncated = false for a normal end_turn completion")
	}
}

// TestAnthropicClientChatStreamDetectsTruncation is
// TestAnthropicClientChatDetectsTruncation's streaming counterpart — the
// stop_reason arrives on the final message_delta event's own delta object
// rather than a top-level response field (see anthropicStreamEvent.Delta's
// doc comment).
func TestAnthropicClientChatStreamDetectsTruncation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		events := []string{
			`{"type":"message_start","message":{"usage":{"input_tokens":8,"output_tokens":0}}}`,
			`{"type":"content_block_delta","delta":{"type":"text_delta","text":"partial"}}`,
			`{"type":"message_delta","delta":{"stop_reason":"max_tokens"},"usage":{"output_tokens":1024}}`,
			`{"type":"message_stop"}`,
		}
		for _, e := range events {
			w.Write([]byte("data: " + e + "\n\n"))
		}
	}))
	defer srv.Close()

	c := NewAnthropicClient(srv.URL, "test-key")
	result, err := c.ChatStream(context.Background(), ChatRequest{
		Model:    "claude-sonnet-5",
		Messages: []Message{{Role: "user", Content: "hi"}},
	}, func(string) {})
	if err != nil {
		t.Fatalf("ChatStream() error = %v", err)
	}
	if !result.Truncated {
		t.Fatal("expected Truncated = true when the final message_delta's stop_reason is max_tokens")
	}
}

func TestAnthropicClientChatError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"message": "invalid x-api-key"}})
	}))
	defer srv.Close()

	c := NewAnthropicClient(srv.URL, "bad-key")
	_, err := c.Chat(context.Background(), ChatRequest{Model: "claude-sonnet-5", Messages: []Message{{Role: "user", Content: "hi"}}})
	if err == nil {
		t.Fatal("expected error for 401 response, got nil")
	}
}

func TestAnthropicClientChatStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req anthropicRequest
		json.NewDecoder(r.Body).Decode(&req)
		if !req.Stream {
			t.Error("expected stream=true in request")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		events := []string{
			`{"type":"message_start","message":{"usage":{"input_tokens":8,"output_tokens":0}}}`,
			`{"type":"content_block_delta","delta":{"type":"text_delta","text":"Hello"}}`,
			`{"type":"content_block_delta","delta":{"type":"text_delta","text":" world"}}`,
			`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}`,
			`{"type":"message_stop"}`,
		}
		for _, e := range events {
			w.Write([]byte("data: " + e + "\n\n"))
		}
	}))
	defer srv.Close()

	c := NewAnthropicClient(srv.URL, "test-key")
	var deltas []string
	result, err := c.ChatStream(context.Background(), ChatRequest{
		Model:    "claude-sonnet-5",
		Messages: []Message{{Role: "user", Content: "hi"}},
	}, func(d string) { deltas = append(deltas, d) })
	if err != nil {
		t.Fatalf("ChatStream() error = %v", err)
	}
	if result.Content != "Hello world" {
		t.Fatalf("content = %q, want %q", result.Content, "Hello world")
	}
	if strings.Join(deltas, "") != "Hello world" {
		t.Fatalf("deltas joined = %q, want %q", strings.Join(deltas, ""), "Hello world")
	}
	if result.TokensIn != 8 || result.TokensOut != 3 {
		t.Fatalf("tokens = (%d, %d), want (8, 3)", result.TokensIn, result.TokensOut)
	}
}
