package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAnthropicClientComplete(t *testing.T) {
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
		})
	}))
	defer srv.Close()

	c := NewAnthropicClient(srv.URL, "test-key", "claude-sonnet-5")
	answer, err := c.Complete(context.Background(), "be helpful", "what is onebox?")
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if answer != "an all-in-one AI backend" {
		t.Fatalf("answer = %q, want %q", answer, "an all-in-one AI backend")
	}
}

func TestAnthropicClientCompleteError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"message": "invalid x-api-key"}})
	}))
	defer srv.Close()

	c := NewAnthropicClient(srv.URL, "bad-key", "claude-sonnet-5")
	_, err := c.Complete(context.Background(), "sys", "hello")
	if err == nil {
		t.Fatal("expected error for 401 response, got nil")
	}
}
