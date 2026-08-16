// Package llm provides a provider-agnostic chat-completion gateway:
// Anthropic, OpenAI, and Ollama adapters behind one interface, so
// /api/llm/chat and the RAG engine's /api/rag/answer both just send
// {model, messages} without caring which backend serves it.
package llm

import (
	"context"
	"encoding/json"
	"time"
)

type Message struct {
	Role    string `json:"role"` // "system", "user", "assistant", or "tool"
	Content string `json:"content"`
	// Images are optional vision attachments for this message — empty for
	// every ordinary text-only turn (which, today, is still every message
	// sent through POST /api/llm/chat — only the admin chatbot's
	// attachment pipeline populates this, see resolveAttachments in
	// internal/server/chatbot_attachments.go). Tagged json:"-" because the
	// generic /api/llm/chat gateway decodes ChatRequest.Messages straight
	// from the caller's JSON body and has no wire format for images yet;
	// this field only gets populated in Go, never from that endpoint's
	// request body. See MessageImage for how each Provider translates it.
	Images []MessageImage `json:"-"`
	// ToolCalls, set only on a Role:"assistant" message being replayed back
	// as conversation history after a tool-execution round (see the
	// multi-round loop in internal/server/chatbot_handlers.go), are the
	// exact calls that assistant turn made. Each Provider reconstructs its
	// own native assistant-turn-with-tool-calls wire shape from this
	// (Anthropic's tool_use content blocks, OpenAI/Ollama's tool_calls
	// field) — required so the *next* call's tool-result message has
	// something to attach to; a provider cannot accept a tool-result
	// message that doesn't follow a matching tool call in the same
	// conversation.
	ToolCalls []ToolCall `json:"-"`
	// ToolCallID, set only on a Role:"tool" message, names which ToolCall
	// (by ID) from the immediately preceding assistant turn this message
	// is the result of — every provider's native tool-result wire format
	// requires this linkage. Content on a "tool" message is the tool's
	// plain-text result (see chatbot_tool_execution.go's toolResult).
	// Ollama calls send no ID at all (see ToolCall's doc comment); for
	// those, providers fall back to positional/name matching rather than
	// requiring a non-empty ToolCallID.
	ToolCallID string `json:"-"`
}

// MessageImage is one image attachment on a Message, already decoded to
// raw bytes. Each Provider's Chat/ChatStream translates this into that
// provider's own wire shape instead of Message being marshaled directly:
//   - Ollama: a flat base64 string appended to the message's "images" array
//     (see toOllamaMessages in ollama.go)
//   - OpenAI: a "data:<MediaType>;base64,<...>" URI inside a content-part
//     array (see toOpenAIMessages in openai.go)
//   - Anthropic: a base64 "source" block inside a content-block array (see
//     anthropicContent in anthropic.go)
//
// Keeping this provider-agnostic (raw bytes + a MIME type, nothing more)
// is what lets internal/server build one Images slice per message without
// caring which provider ends up receiving it.
type MessageImage struct {
	MediaType string // e.g. "image/png", "image/jpeg"
	Data      []byte // raw (not base64-encoded) image bytes
}

// Tool describes one function the model may call natively during a chat
// turn — the provider-agnostic shape every Provider translates into its
// own wire format (Anthropic's top-level "tools" array with
// "input_schema", OpenAI's "tools" array of {type:"function", function},
// Ollama's identically-shaped "tools" array). Schema is plain JSON Schema
// (the "object/properties/required" subset every provider here accepts
// unmodified) describing the tool's arguments — see
// internal/server/chatbot_actions.go for the concrete tools this codebase
// offers. Description is the only channel that tells the model when to
// call this tool instead of just answering in prose; there is no other
// per-tool signal, so every caller writes one that names the concrete
// admin request it answers, not just its own name.
type Tool struct {
	Name        string
	Description string
	Schema      json.RawMessage
}

// ToolCall is one function call the model decided to make, translated
// back from whichever provider-specific wire shape it arrived in via that
// provider's own *native* tool-calling channel — never inferred from
// prose. Arguments is that provider's own decoded JSON for this call's
// parameters, already a well-formed json.RawMessage object (a second
// json.Unmarshal into the matching payload struct is ActionParser's job —
// see chatbot_actions.go's strictUnmarshal). One known wire quirk from a
// local Ollama model re-stringifies nested array/object argument values;
// that is compensated for downstream (see
// internal/server/chatbot_actions_compat.go), never inside a Provider —
// Provider implementations only ever decode what the API actually sent,
// nothing more. ID is the provider's own call identifier when it supplies
// one (Anthropic, OpenAI); Ollama sends none, so ID is empty for calls
// that came from it — nothing downstream may depend on ID being non-empty.
type ToolCall struct {
	ID        string
	Name      string
	Arguments json.RawMessage
}

type ChatRequest struct {
	Model    string
	Messages []Message
	// Tools are the functions the model may call natively this turn — nil
	// (the overwhelming common case: every lightweight-greeting fast-path
	// request, the generic /api/llm/chat gateway, /api/rag/answer) means
	// exactly what it always meant before Tool existed: a plain chat
	// completion with no function-calling offered at all. Only the admin
	// chatbot's full (non-greeting) turn populates this — see
	// answerChatbotQuestion and actionToolDefs.
	Tools []Tool
}

// ChatTiming is optional, provider-specific sub-request timing detail —
// today only OllamaClient.Chat populates it, to back the admin chatbot's
// per-request profiling log (see answerChatbotQuestion in
// internal/server/chatbot_handlers.go). Anthropic/OpenAI callers are
// unaffected and simply leave this as the zero value; nothing here changes
// what Content/TokensIn/TokensOut mean or how existing callers use them.
type ChatTiming struct {
	// RequestBuild is time spent marshaling the request body and building
	// the *http.Request — before anything is sent over the wire.
	RequestBuild time.Duration
	// TimeToFirstByte is the wall-clock time from sending the HTTP request
	// to the client's Do() call returning (i.e. response headers
	// received). For a non-streaming Ollama call (stream:false), Ollama
	// does not write any response bytes until generation is completely
	// finished — so for that provider, this single interval covers
	// network latency + prompt evaluation + full token generation, not
	// just "network time." That's expected today; it will only separate
	// out once streaming is implemented.
	TimeToFirstByte time.Duration
	// TotalDuration is send-to-fully-decoded — TimeToFirstByte plus
	// whatever it took to read and JSON-decode the response body.
	TotalDuration time.Duration
}

// ChatResult carries token counts alongside the answer so callers (usage
// logging, spend caps) don't need provider-specific response parsing.
type ChatResult struct {
	Content   string
	TokensIn  int
	TokensOut int
	Timing    ChatTiming
	// ToolCalls are populated only from a provider's *native* structured
	// tool-calling response field (Anthropic's tool_use content blocks,
	// OpenAI's message.tool_calls, Ollama's message.tool_calls) — never
	// guessed at from Content. Empty on every turn where the model didn't
	// call anything, which is the normal, valid outcome for a plain
	// question (see chatbot_actions.go's ActionParser doc comment) — never
	// treat an empty ToolCalls as an error.
	ToolCalls []ToolCall
	// Truncated reports whether the provider itself says this reply was cut
	// off by the max-tokens cap rather than reaching a natural stopping
	// point — Anthropic's stop_reason == "max_tokens" (see AnthropicClient's
	// Chat/ChatStream). A truncated reply's tool-call JSON (if it was mid
	// tool_use block) will typically fail to unmarshal downstream, which
	// previously surfaced as an opaque "could not be validated" with no clue
	// the real cause was the token cap. Only Anthropic populates this today;
	// false is the correct zero value for every other provider/response.
	Truncated bool
}

// Provider is a single chat-completion backend.
type Provider interface {
	Chat(ctx context.Context, req ChatRequest) (ChatResult, error)
	// ChatStream calls onDelta with each incremental text fragment as it
	// arrives, and still returns the full accumulated ChatResult at the
	// end (with usage), so callers don't have to reassemble it themselves.
	ChatStream(ctx context.Context, req ChatRequest, onDelta func(string)) (ChatResult, error)
}
