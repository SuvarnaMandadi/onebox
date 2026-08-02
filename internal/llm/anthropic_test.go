package llm

import (
	"context"
	"encoding/json"
	"io"
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
		json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{{"type": "text", "text": "an all-in-one AI backend"}},
			"usage":   map[string]int{"input_tokens": 10, "output_tokens": 5},
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

// TestAnthropicClientChatToolCall pins the non-streaming tool-calling
// contract: a Tool offered on the request must appear in Anthropic's own
// `tools`/`input_schema` wire shape, and a `tool_use` content block on the
// response must come back as a fully-structured ToolCall — Arguments
// already a decoded JSON object, not text the caller has to parse.
func TestAnthropicClientChatToolCall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"name":"create_collection"`) {
			t.Errorf("request missing tool declaration: %s", body)
		}
		if !strings.Contains(string(body), `"input_schema"`) {
			t.Errorf("request missing input_schema: %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": "Here's a proposed schema."},
				{"type": "tool_use", "id": "toolu_01", "name": "create_collection", "input": map[string]any{"name": "notes"}},
			},
			"usage": map[string]int{"input_tokens": 10, "output_tokens": 5},
		})
	}))
	defer srv.Close()

	c := NewAnthropicClient(srv.URL, "test-key")
	result, err := c.Chat(context.Background(), ChatRequest{
		Model:    "claude-sonnet-5",
		Messages: []Message{{Role: "user", Content: "create a notes collection"}},
		Tools:    []Tool{{Name: "create_collection", Description: "Propose creating a collection", Schema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}}}`)}},
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if result.Content != "Here's a proposed schema." {
		t.Fatalf("content = %q, want the accompanying text, unaffected by the tool call", result.Content)
	}
	if len(result.ToolCalls) != 1 {
		t.Fatalf("ToolCalls = %d, want 1: %+v", len(result.ToolCalls), result.ToolCalls)
	}
	tc := result.ToolCalls[0]
	if tc.Name != "create_collection" || tc.ID != "toolu_01" {
		t.Fatalf("unexpected tool call: %+v", tc)
	}
	var args map[string]string
	if err := json.Unmarshal(tc.Arguments, &args); err != nil {
		t.Fatalf("Arguments not valid JSON: %v (%s)", err, tc.Arguments)
	}
	if args["name"] != "notes" {
		t.Fatalf("args[name] = %q, want notes", args["name"])
	}
}

// TestAnthropicClientChatStreamToolCall pins the streaming tool-calling
// contract: a tool call's id/name (from content_block_start) and its
// arguments (assembled from possibly-many input_json_delta fragments)
// must be correlated by block index into one complete ToolCall — and
// none of those fragments must ever reach onDelta, since they're
// structured data, not chat text.
func TestAnthropicClientChatStreamToolCall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		events := []string{
			`{"type":"message_start","message":{"usage":{"input_tokens":12,"output_tokens":0}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"text"}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Sure, "}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"here you go."}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_02","name":"create_collection"}}`,
			`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"name\":"}}`,
			`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"notes\"}"}}`,
			`{"type":"content_block_stop","index":1}`,
			`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":9}}`,
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
		Messages: []Message{{Role: "user", Content: "create a notes collection"}},
		Tools:    []Tool{{Name: "create_collection", Schema: json.RawMessage(`{"type":"object"}`)}},
	}, func(d string) { deltas = append(deltas, d) })
	if err != nil {
		t.Fatalf("ChatStream() error = %v", err)
	}
	if result.Content != "Sure, here you go." {
		t.Fatalf("content = %q, want %q", result.Content, "Sure, here you go.")
	}
	if strings.Join(deltas, "") != "Sure, here you go." {
		t.Fatalf("deltas leaked non-text content: %q", strings.Join(deltas, ""))
	}
	if len(result.ToolCalls) != 1 {
		t.Fatalf("ToolCalls = %d, want 1: %+v", len(result.ToolCalls), result.ToolCalls)
	}
	tc := result.ToolCalls[0]
	if tc.ID != "toolu_02" || tc.Name != "create_collection" {
		t.Fatalf("unexpected tool call: %+v", tc)
	}
	var args map[string]string
	if err := json.Unmarshal(tc.Arguments, &args); err != nil {
		t.Fatalf("Arguments not valid JSON after reassembly: %v (%s)", err, tc.Arguments)
	}
	if args["name"] != "notes" {
		t.Fatalf("args[name] = %q, want notes", args["name"])
	}
	if result.TokensIn != 12 || result.TokensOut != 9 {
		t.Fatalf("tokens = (%d, %d), want (12, 9)", result.TokensIn, result.TokensOut)
	}
}
