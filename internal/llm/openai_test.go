package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAIClientChat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path = %q, want /chat/completions", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer test-key")
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(openAIChatResponse{
			Choices: []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			}{{Message: struct {
				Content string `json:"content"`
			}{Content: "hi there"}}},
			Usage: openAIUsage{PromptTokens: 4, CompletionTokens: 2},
		})
	}))
	defer srv.Close()

	c := NewOpenAIClient(srv.URL, "test-key")
	result, err := c.Chat(context.Background(), ChatRequest{Model: "gpt-4o", Messages: []Message{{Role: "user", Content: "hello"}}})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if result.Content != "hi there" {
		t.Fatalf("content = %q, want %q", result.Content, "hi there")
	}
	if result.TokensIn != 4 || result.TokensOut != 2 {
		t.Fatalf("tokens = (%d, %d), want (4, 2)", result.TokensIn, result.TokensOut)
	}
}

func TestOpenAIClientChatStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		lines := []string{
			`{"choices":[{"delta":{"content":"Hi"}}]}`,
			`{"choices":[{"delta":{"content":" there"}}]}`,
			`{"choices":[],"usage":{"prompt_tokens":3,"completion_tokens":2}}`,
			`[DONE]`,
		}
		for _, l := range lines {
			w.Write([]byte("data: " + l + "\n\n"))
		}
	}))
	defer srv.Close()

	c := NewOpenAIClient(srv.URL, "test-key")
	var deltas []string
	result, err := c.ChatStream(context.Background(), ChatRequest{Model: "gpt-4o", Messages: []Message{{Role: "user", Content: "hi"}}}, func(d string) {
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
	if result.TokensIn != 3 || result.TokensOut != 2 {
		t.Fatalf("tokens = (%d, %d), want (3, 2)", result.TokensIn, result.TokensOut)
	}
}
