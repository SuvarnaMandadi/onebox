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
			Content: []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}{{Type: "text", Text: "an all-in-one AI backend"}},
			Usage: anthropicUsage{InputTokens: 10, OutputTokens: 5},
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
