package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// OllamaClient calls a local Ollama daemon's /api/chat endpoint.
type OllamaClient struct {
	BaseURL string
	Client  *http.Client
}

func NewOllamaClient(baseURL string) *OllamaClient {
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	return &OllamaClient{BaseURL: baseURL, Client: http.DefaultClient}
}

type ollamaChatRequest struct {
	Model    string          `json:"model"`
	Messages []ollamaMessage `json:"messages"`
	Stream   bool            `json:"stream"`
	Tools    []ollamaTool    `json:"tools,omitempty"`
}

// ollamaTool is Ollama's wire shape for one offered tool — a nested
// {type:"function", function:{name, description, parameters}} object,
// modeled on (and identical in shape to) OpenAI's own tool format; see
// openAITool in openai.go for that provider's independent copy of the same
// shape (kept separate rather than shared, matching this codebase's
// existing per-provider wire-type convention — see ollamaMessage vs
// openAIMessage).
type ollamaTool struct {
	Type     string             `json:"type"` // always "function"
	Function ollamaToolFunction `json:"function"`
}

type ollamaToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

// toOllamaTools converts the provider-agnostic Tool list to Ollama's wire
// shape. Returns nil (omitted via omitempty above) for the common
// no-tools case, so a plain chat request's body is byte-for-byte unchanged
// from before tool-calling existed.
func toOllamaTools(tools []Tool) []ollamaTool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]ollamaTool, len(tools))
	for i, t := range tools {
		out[i] = ollamaTool{Type: "function", Function: ollamaToolFunction{Name: t.Name, Description: t.Description, Parameters: t.Schema}}
	}
	return out
}

// ollamaMessage is Ollama's wire shape for a chat message — Content plus a
// flat sibling "images" array of raw base64 strings (no "data:" URI
// prefix, unlike OpenAI/Anthropic). See toOllamaMessages.
type ollamaMessage struct {
	Role    string   `json:"role"`
	Content string   `json:"content"`
	Images  []string `json:"images,omitempty"`
	// ToolCalls, set only on a role:"assistant" message being replayed as
	// history after a tool-execution round (see toOllamaMessages), is
	// Ollama's native tool_calls field. A following role:"tool" message
	// needs no linkage field here — unlike OpenAI/Anthropic, Ollama's
	// tool-result convention is a plain {role:"tool", content} message
	// with no call ID at all (see ToolCall's doc comment in llm.go).
	ToolCalls []ollamaRequestToolCall `json:"tool_calls,omitempty"`
}

// ollamaRequestToolCall is Ollama's wire shape for one tool call inside an
// outgoing assistant-message's tool_calls array. Unlike
// openAIRequestToolCall, Arguments is a native JSON object (not a
// JSON-encoded string) and there is no ID field — mirroring how a response
// tool call arrives (see ollamaToolCallWire).
type ollamaRequestToolCall struct {
	Function struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"function"`
}

// toOllamaRequestToolCalls converts the provider-agnostic ToolCall list
// (from Message.ToolCalls) into Ollama's outgoing wire shape.
func toOllamaRequestToolCalls(calls []ToolCall) []ollamaRequestToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]ollamaRequestToolCall, len(calls))
	for i, c := range calls {
		out[i].Function.Name = c.Name
		out[i].Function.Arguments = c.Arguments
	}
	return out
}

// toOllamaMessages converts the provider-agnostic Message list to Ollama's
// wire shape, base64-encoding any Message.Images into the flat "images"
// field Ollama expects alongside Content, and translating Message.ToolCalls
// (an assistant turn being replayed after a tool-execution round) into the
// native tool_calls field. A Role:"tool" message (a tool's result) needs no
// special handling beyond its plain Role/Content — see ollamaMessage's doc
// comment. Messages with none of this marshal identically to before these
// fields existed.
func toOllamaMessages(messages []Message) []ollamaMessage {
	out := make([]ollamaMessage, len(messages))
	for i, m := range messages {
		om := ollamaMessage{Role: m.Role, Content: m.Content, ToolCalls: toOllamaRequestToolCalls(m.ToolCalls)}
		for _, img := range m.Images {
			om.Images = append(om.Images, base64.StdEncoding.EncodeToString(img.Data))
		}
		out[i] = om
	}
	return out
}

// ollamaToolCallWire is Ollama's wire shape for one tool call the model
// decided to make. Unlike OpenAI, Arguments arrives as a native JSON
// object (not a JSON-encoded string) and there is no call ID at all — see
// ToolCall's doc comment. A separate, real quirk (some local models
// re-stringify a *nested* array/object value inside Arguments) is
// compensated for downstream, not here — see
// internal/server/chatbot_actions_compat.go.
type ollamaToolCallWire struct {
	Function struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"function"`
}

type ollamaChatChunk struct {
	// Message is named (rather than an inline anonymous struct, as this
	// used to be) so ollama_test.go can construct one directly instead of
	// re-declaring its exact field set at every call site.
	Message         ollamaChatMessageWire `json:"message"`
	Done            bool                  `json:"done"`
	PromptEvalCount int                   `json:"prompt_eval_count"`
	EvalCount       int                   `json:"eval_count"`
	Error           string                `json:"error"`
}

type ollamaChatMessageWire struct {
	Content   string               `json:"content"`
	ToolCalls []ollamaToolCallWire `json:"tool_calls,omitempty"`
}

// toOllamaToolCalls converts Ollama's wire-shape tool calls to the
// provider-agnostic ToolCall list. Returns nil for the common
// no-tool-call case.
func toOllamaToolCalls(wire []ollamaToolCallWire) []ToolCall {
	if len(wire) == 0 {
		return nil
	}
	out := make([]ToolCall, len(wire))
	for i, w := range wire {
		out[i] = ToolCall{Name: w.Function.Name, Arguments: w.Function.Arguments}
	}
	return out
}

func (c *OllamaClient) newRequest(ctx context.Context, body []byte) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

type ollamaTagsResponse struct {
	Models []struct {
		Name string `json:"name"`
	} `json:"models"`
}

// ListModels reports the tags (e.g. "llama3.2:3b") of every model
// currently pulled into this Ollama daemon, straight from its own
// GET /api/tags — the same endpoint `ollama list` uses — so the Chat
// Provider settings panel can offer a dropdown of what's actually
// installed instead of a free-text field a self-hoster has to get exactly
// right.
func (c *OllamaClient) ListModels(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/api/tags", nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	resp, err := c.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list models: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama returned status %d listing models", resp.StatusCode)
	}

	var parsed ollamaTagsResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("decode tags response: %w", err)
	}
	names := make([]string, len(parsed.Models))
	for i, m := range parsed.Models {
		names[i] = m.Name
	}
	return names, nil
}

// embeddingModelSubstrings are name fragments (matched against the model
// tag, lowercased, with any ":version" suffix stripped) that identify an
// Ollama model as embedding-only. This is a heuristic, not a registry
// lookup — /api/tags reports names, not capabilities — but it covers every
// embedding family onebox itself documents as an example for the Embedding
// Provider (nomic-embed-text, bge, e5, voyage) plus the other common ones
// (gte, minilm), so a model pulled for RAG/embeddings never shows up as a
// choice in the Chat Provider's model dropdown. See IsEmbeddingModel.
var embeddingModelSubstrings = []string{
	"embed",
	"bge",
	"e5",
	"gte",
	"minilm",
	"voyage",
}

// IsEmbeddingModel reports whether an Ollama model name looks like an
// embedding-only model rather than a chat model. Embedding-only models are
// never chat-capable, so this backs both ListChatModels and the settings
// save path's validation (see validateChatModel in the server package) —
// two independent guards against the same failure mode: a model like
// "nomic-embed-text" ending up selected as the dashboard's own chat model.
func IsEmbeddingModel(name string) bool {
	base := strings.ToLower(name)
	if i := strings.Index(base, ":"); i >= 0 {
		base = base[:i]
	}
	for _, frag := range embeddingModelSubstrings {
		if strings.Contains(base, frag) {
			return true
		}
	}
	return false
}

// ListChatModels is ListModels filtered down to models not recognized as
// embedding-only (see IsEmbeddingModel) — what the Chat Provider settings
// panel's Ollama dropdown should actually offer. Embedding-only models are
// never removed from Ollama itself or from the Embedding Provider's own
// model field; filtering only ever affects what's offered for chat.
func (c *OllamaClient) ListChatModels(ctx context.Context) ([]string, error) {
	all, err := c.ListModels(ctx)
	if err != nil {
		return nil, err
	}
	chatModels := make([]string, 0, len(all))
	for _, m := range all {
		if !IsEmbeddingModel(m) {
			chatModels = append(chatModels, m)
		}
	}
	return chatModels, nil
}

type ollamaVersionResponse struct {
	Version string `json:"version"`
}

// Version reports the running Ollama daemon's version string via its
// GET /api/version endpoint, so the dashboard can show what's actually
// installed rather than assuming a version.
func (c *OllamaClient) Version(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/api/version", nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	resp, err := c.Client.Do(req)
	if err != nil {
		return "", fmt.Errorf("get version: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ollama returned status %d fetching version", resp.StatusCode)
	}

	var parsed ollamaVersionResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", fmt.Errorf("decode version response: %w", err)
	}
	return parsed.Version, nil
}

func (c *OllamaClient) Chat(ctx context.Context, req ChatRequest) (ChatResult, error) {
	buildStart := time.Now()
	body, err := json.Marshal(ollamaChatRequest{Model: req.Model, Messages: toOllamaMessages(req.Messages), Stream: false, Tools: toOllamaTools(req.Tools)})
	if err != nil {
		return ChatResult{}, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := c.newRequest(ctx, body)
	if err != nil {
		return ChatResult{}, err
	}
	requestBuild := time.Since(buildStart)

	// sendStart is the "request sent" instant both TimeToFirstByte and
	// TotalDuration are measured from. Do() blocks until the response
	// headers arrive — with stream:false, Ollama holds the connection open
	// and writes nothing until generation is fully done, so on this
	// provider ttfb effectively equals network + prompt-eval + generation
	// time combined, not just network time. See ChatTiming's doc comment.
	sendStart := time.Now()
	resp, err := c.Client.Do(httpReq)
	ttfb := time.Since(sendStart)
	if err != nil {
		return ChatResult{}, fmt.Errorf("chat request: %w", err)
	}
	defer resp.Body.Close()

	var parsed ollamaChatChunk
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return ChatResult{}, fmt.Errorf("decode response: %w", err)
	}
	total := time.Since(sendStart)
	if resp.StatusCode != http.StatusOK {
		if parsed.Error != "" {
			return ChatResult{}, fmt.Errorf("ollama error: %s", parsed.Error)
		}
		return ChatResult{}, fmt.Errorf("ollama returned status %d", resp.StatusCode)
	}

	return ChatResult{
		Content:   parsed.Message.Content,
		ToolCalls: toOllamaToolCalls(parsed.Message.ToolCalls),
		TokensIn:  parsed.PromptEvalCount,
		TokensOut: parsed.EvalCount,
		Timing: ChatTiming{
			RequestBuild:    requestBuild,
			TimeToFirstByte: ttfb,
			TotalDuration:   total,
		},
	}, nil
}

func (c *OllamaClient) ChatStream(ctx context.Context, req ChatRequest, onDelta func(string)) (ChatResult, error) {
	body, err := json.Marshal(ollamaChatRequest{Model: req.Model, Messages: toOllamaMessages(req.Messages), Stream: true, Tools: toOllamaTools(req.Tools)})
	if err != nil {
		return ChatResult{}, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := c.newRequest(ctx, body)
	if err != nil {
		return ChatResult{}, err
	}

	resp, err := c.Client.Do(httpReq)
	if err != nil {
		return ChatResult{}, fmt.Errorf("chat request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var parsed ollamaChatChunk
		json.NewDecoder(resp.Body).Decode(&parsed)
		if parsed.Error != "" {
			return ChatResult{}, fmt.Errorf("ollama error: %s", parsed.Error)
		}
		return ChatResult{}, fmt.Errorf("ollama returned status %d", resp.StatusCode)
	}

	// Ollama streams newline-delimited JSON objects, not SSE.
	var result ChatResult
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var chunk ollamaChatChunk
		if err := json.Unmarshal(line, &chunk); err != nil {
			continue
		}
		if chunk.Message.Content != "" {
			result.Content += chunk.Message.Content
			onDelta(chunk.Message.Content)
		}
		// Unlike OpenAI/Anthropic, Ollama does not fragment a tool call's
		// arguments across chunks — when the model decides to call a tool,
		// the whole tool_calls array arrives complete in one chunk, so this
		// is a straight append, never an accumulate-then-finalize dance.
		if len(chunk.Message.ToolCalls) > 0 {
			result.ToolCalls = append(result.ToolCalls, toOllamaToolCalls(chunk.Message.ToolCalls)...)
		}
		if chunk.Done {
			result.TokensIn = chunk.PromptEvalCount
			result.TokensOut = chunk.EvalCount
		}
	}
	if err := scanner.Err(); err != nil {
		return result, fmt.Errorf("read stream: %w", err)
	}
	return result, nil
}
