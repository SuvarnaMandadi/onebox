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

func TestOpenAIClientChat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path = %q, want /chat/completions", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer test-key")
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": "hi there"}}},
			"usage":   map[string]int{"prompt_tokens": 4, "completion_tokens": 2},
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

// TestOpenAIClientChatToolCall pins the non-streaming tool-calling
// contract, including OpenAI's one real quirk versus Anthropic/Ollama:
// arguments arrive as a JSON-encoded *string*, which must come out the
// other end as a parseable json.RawMessage, not double-encoded text.
func TestOpenAIClientChatToolCall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"type":"function"`) || !strings.Contains(string(body), `"name":"create_collection"`) {
			t.Errorf("request missing function tool declaration: %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{
				"content": "",
				"tool_calls": []map[string]any{
					{"id": "call_01", "type": "function", "function": map[string]string{
						"name": "create_collection", "arguments": `{"name":"notes"}`,
					}},
				},
			}}},
			"usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 5},
		})
	}))
	defer srv.Close()

	c := NewOpenAIClient(srv.URL, "test-key")
	result, err := c.Chat(context.Background(), ChatRequest{
		Model:    "gpt-4o",
		Messages: []Message{{Role: "user", Content: "create a notes collection"}},
		Tools:    []Tool{{Name: "create_collection", Schema: json.RawMessage(`{"type":"object"}`)}},
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if len(result.ToolCalls) != 1 {
		t.Fatalf("ToolCalls = %d, want 1: %+v", len(result.ToolCalls), result.ToolCalls)
	}
	tc := result.ToolCalls[0]
	if tc.ID != "call_01" || tc.Name != "create_collection" {
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

// TestOpenAIClientChatStreamToolCall pins reassembly of a tool call whose
// id/name and JSON-string arguments both arrive fragmented across
// several delta.tool_calls chunks, correlated by Index — and that none
// of those fragments ever reach onDelta.
func TestOpenAIClientChatStreamToolCall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		lines := []string{
			`{"choices":[{"delta":{"content":"One sec."}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_02","function":{"name":"create_collection","arguments":""}}]}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"name\":"}}]}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"notes\"}"}}]}}]}`,
			`{"choices":[],"usage":{"prompt_tokens":6,"completion_tokens":4}}`,
			`[DONE]`,
		}
		for _, l := range lines {
			w.Write([]byte("data: " + l + "\n\n"))
		}
	}))
	defer srv.Close()

	c := NewOpenAIClient(srv.URL, "test-key")
	var deltas []string
	result, err := c.ChatStream(context.Background(), ChatRequest{
		Model:    "gpt-4o",
		Messages: []Message{{Role: "user", Content: "create a notes collection"}},
		Tools:    []Tool{{Name: "create_collection", Schema: json.RawMessage(`{"type":"object"}`)}},
	}, func(d string) { deltas = append(deltas, d) })
	if err != nil {
		t.Fatalf("ChatStream() error = %v", err)
	}
	if strings.Join(deltas, "") != "One sec." {
		t.Fatalf("deltas leaked tool-call fragments: %q", strings.Join(deltas, ""))
	}
	if len(result.ToolCalls) != 1 {
		t.Fatalf("ToolCalls = %d, want 1: %+v", len(result.ToolCalls), result.ToolCalls)
	}
	tc := result.ToolCalls[0]
	if tc.ID != "call_02" || tc.Name != "create_collection" {
		t.Fatalf("unexpected tool call: %+v", tc)
	}
	var args map[string]string
	if err := json.Unmarshal(tc.Arguments, &args); err != nil {
		t.Fatalf("Arguments not valid JSON after reassembly: %v (%s)", err, tc.Arguments)
	}
	if args["name"] != "notes" {
		t.Fatalf("args[name] = %q, want notes", args["name"])
	}
}
