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
	Role    string `json:"role"` // "system", "user", or "assistant"
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

// Tool is a provider-agnostic function/tool declaration — a caller that
// wants structured output back (see internal/server/chatbot_actions.go)
// describes it once here and each Provider translates it into that API's
// own native tool/function-calling wire shape (Anthropic's `tools` +
// `input_schema`, OpenAI's `tools` + `function.parameters`, Ollama's
// `tools`, which mirrors OpenAI's). The model decides for itself whether
// and when to call it; nothing here forces a call.
type Tool struct {
	// Name is both the wire-level tool name the provider echoes back on a
	// ToolCall and, for the admin chatbot's use, the same string as the
	// resulting proposedAction.Type (see actionToolDefs) — one identifier,
	// no separate mapping table to keep in sync.
	Name string
	// Description is the only thing that tells the model when to call this
	// tool — there is no other channel (see Tool's doc comment above), so
	// it needs to name the concrete situation ("the admin asked to create
	// a new collection"), not just restate the tool's name.
	Description string
	// Schema is a JSON Schema object (draft-07-compatible subset — every
	// provider here accepts the same "object/properties/required" shape)
	// describing the tool's arguments. Raw JSON rather than a Go struct
	// because each provider embeds it as-is in its own request body.
	Schema json.RawMessage
}

// ToolCall is one invocation of a Tool the model chose to make, as
// reported back by the provider — already fully structured (Arguments is
// the provider's own decoded JSON object for that call, not text the
// caller has to parse out of a reply). ID is the provider's own call
// identifier where one exists — Anthropic and OpenAI always mint one;
// Ollama does too as of the version this was built/tested against
// (0.32.1), though its API makes no guarantee across every model/version,
// so treat ID as "populated when the provider bothers to send one," not
// a value to depend on. Round-tripped for providers whose API requires
// echoing it back on a follow-up turn; unused otherwise.
type ToolCall struct {
	ID        string
	Name      string
	Arguments json.RawMessage
}

type ChatRequest struct {
	Model    string
	Messages []Message
	// Tools are optionally offered to the model this turn — see Tool's
	// doc comment. Omitted (nil) for every ordinary request; only the
	// admin chatbot's full path attaches the action tool defs today (see
	// chatbot_actions.go).
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
	// ToolCalls is every tool the model invoked this turn (see Tool),
	// already parsed into structured Arguments — empty for any request
	// that didn't offer Tools, and often empty even when it did, since
	// calling a tool is always the model's choice. Content and ToolCalls
	// are independent: a provider may return accompanying text alongside
	// a tool call, only a tool call, or only text — callers must not
	// assume one implies something about the other.
	ToolCalls []ToolCall
}

// Provider is a single chat-completion backend.
type Provider interface {
	Chat(ctx context.Context, req ChatRequest) (ChatResult, error)
	// ChatStream calls onDelta with each incremental text fragment as it
	// arrives, and still returns the full accumulated ChatResult at the
	// end (with usage), so callers don't have to reassemble it themselves.
	ChatStream(ctx context.Context, req ChatRequest, onDelta func(string)) (ChatResult, error)
}
