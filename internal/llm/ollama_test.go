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
			Message: struct {
				Content string `json:"content"`
			}{Content: "hi from llama"},
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
			{Message: struct {
				Content string `json:"content"`
			}{Content: "Hi"}},
			{Message: struct {
				Content string `json:"content"`
			}{Content: " there"}},
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
