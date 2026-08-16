package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
)

const defaultMaxTokens = 1024

// AnthropicClient calls the Anthropic Messages API.
type AnthropicClient struct {
	BaseURL string
	APIKey  string
	Client  *http.Client
}

func NewAnthropicClient(baseURL, apiKey string) *AnthropicClient {
	if baseURL == "" {
		baseURL = "https://api.anthropic.com"
	}
	return &AnthropicClient{BaseURL: baseURL, APIKey: apiKey, Client: http.DefaultClient}
}

// anthropicMessage's Content is `any` because Anthropic accepts either a
// plain string (ordinary text-only messages — the shape every message used
// before image support existed) or a content-block array (required the
// moment a message carries an image — a plain string can't be mixed with
// an image block). See anthropicContent.
type anthropicMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type anthropicContentBlock struct {
	Type   string                `json:"type"` // "text", "image", "tool_use", or "tool_result"
	Text   string                `json:"text,omitempty"`
	Source *anthropicImageSource `json:"source,omitempty"`
	// ID/Name/Input populate a "tool_use" block — used both when parsing a
	// non-streaming response (see anthropicResponseBlock, a separate type)
	// and when replaying an assistant's prior tool call back as history
	// (see toAnthropicMessage).
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
	// ToolUseID/Content populate a "tool_result" block — Anthropic's wire
	// convention for supplying a tool's result back to the model. Per
	// Anthropic's API, a tool_result block is sent inside a role:"user"
	// message (never role:"tool" — Anthropic has no such role), linked to
	// the matching tool_use block by ToolUseID. See toAnthropicMessage.
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
}

type anthropicImageSource struct {
	Type      string `json:"type"` // "base64"
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

// anthropicContent renders a Message's content in Anthropic's wire shape:
// the plain string Content itself for an ordinary text-only message (byte-
// for-byte identical to before image support existed), or a content-block
// array — a leading text block (if Content is non-empty) followed by one
// base64 image block per attachment — once the message carries images.
func anthropicContent(m Message) any {
	if len(m.Images) == 0 {
		return m.Content
	}
	blocks := make([]anthropicContentBlock, 0, len(m.Images)+1)
	if m.Content != "" {
		blocks = append(blocks, anthropicContentBlock{Type: "text", Text: m.Content})
	}
	for _, img := range m.Images {
		blocks = append(blocks, anthropicContentBlock{
			Type:   "image",
			Source: &anthropicImageSource{Type: "base64", MediaType: img.MediaType, Data: base64.StdEncoding.EncodeToString(img.Data)},
		})
	}
	return blocks
}

type anthropicRequest struct {
	Model     string             `json:"model"`
	System    string             `json:"system,omitempty"`
	MaxTokens int                `json:"max_tokens"`
	Messages  []anthropicMessage `json:"messages"`
	Stream    bool               `json:"stream,omitempty"`
	Tools     []anthropicTool    `json:"tools,omitempty"`
}

// anthropicTool is Anthropic's wire shape for one offered tool — a flat
// {name, description, input_schema} object, unlike OpenAI/Ollama's nested
// {type:"function", function:{...}}. See toAnthropicTools.
type anthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// toAnthropicTools converts the provider-agnostic Tool list to Anthropic's
// wire shape. Returns nil (omitted entirely, via omitempty above) for the
// common no-tools case, so a plain chat request's body is byte-for-byte
// unchanged from before tool-calling existed.
func toAnthropicTools(tools []Tool) []anthropicTool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]anthropicTool, len(tools))
	for i, t := range tools {
		out[i] = anthropicTool{Name: t.Name, Description: t.Description, InputSchema: t.Schema}
	}
	return out
}

type anthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type anthropicResponse struct {
	Content []anthropicResponseBlock `json:"content"`
	Usage   anthropicUsage           `json:"usage"`
	// StopReason is Anthropic's own reason the model stopped generating —
	// "end_turn"/"tool_use" for a normal completion, "max_tokens" when
	// defaultMaxTokens cut it off mid-generation. See Chat's use of this
	// below and ChatResult.Truncated's doc comment (llm.go).
	StopReason string `json:"stop_reason"`
	Error      *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// anthropicResponseBlock is one entry in a non-streaming response's
// content array — named (rather than an inline anonymous struct, as this
// used to be) so anthropic_test.go can construct one directly instead of
// re-declaring its exact field set at every call site.
type anthropicResponseBlock struct {
	Type string `json:"type"` // "text" or "tool_use"
	Text string `json:"text"`
	// ID/Name/Input are only present on a "tool_use" block.
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// splitSystem pulls any "system"-role messages out of the OpenAI-style
// flat message list, since Anthropic takes system instructions as a
// separate top-level field rather than a message with that role.
func splitSystem(messages []Message) (string, []anthropicMessage) {
	var system strings.Builder
	var rest []anthropicMessage
	for _, m := range messages {
		if m.Role == "system" {
			if system.Len() > 0 {
				system.WriteString("\n")
			}
			system.WriteString(m.Content)
			continue
		}
		rest = append(rest, toAnthropicMessage(m))
	}
	return system.String(), rest
}

// toAnthropicMessage translates one Message into Anthropic's wire shape,
// covering the three cases beyond plain text/image content:
//   - Role:"tool" (a tool's result, from the multi-round tool-execution
//     loop in internal/server/chatbot_handlers.go) becomes a role:"user"
//     message with a single tool_result block — Anthropic has no "tool"
//     role; tool_result blocks are conventionally carried inside a user
//     turn, linked back to the matching tool_use block via ToolUseID.
//   - Role:"assistant" with ToolCalls set (replaying a prior turn where
//     the model called one or more tools) becomes an optional leading text
//     block followed by one tool_use block per call — required so the
//     provider has a tool_use block for the following tool_result message
//     to attach to; Anthropic rejects a tool_result with no matching
//     tool_use earlier in the conversation.
//   - Everything else falls back to the pre-existing anthropicContent
//     (plain string, or text+image blocks) unchanged.
func toAnthropicMessage(m Message) anthropicMessage {
	if m.Role == "tool" {
		return anthropicMessage{
			Role: "user",
			Content: []anthropicContentBlock{{
				Type:      "tool_result",
				ToolUseID: m.ToolCallID,
				Content:   m.Content,
			}},
		}
	}
	if len(m.ToolCalls) > 0 {
		blocks := make([]anthropicContentBlock, 0, len(m.ToolCalls)+1)
		if m.Content != "" {
			blocks = append(blocks, anthropicContentBlock{Type: "text", Text: m.Content})
		}
		for _, tc := range m.ToolCalls {
			blocks = append(blocks, anthropicContentBlock{Type: "tool_use", ID: tc.ID, Name: tc.Name, Input: tc.Arguments})
		}
		return anthropicMessage{Role: m.Role, Content: blocks}
	}
	return anthropicMessage{Role: m.Role, Content: anthropicContent(m)}
}

func (c *AnthropicClient) newRequest(ctx context.Context, body []byte) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	return req, nil
}

func (c *AnthropicClient) Chat(ctx context.Context, req ChatRequest) (ChatResult, error) {
	system, messages := splitSystem(req.Messages)
	body, err := json.Marshal(anthropicRequest{Model: req.Model, System: system, MaxTokens: defaultMaxTokens, Messages: messages, Tools: toAnthropicTools(req.Tools)})
	if err != nil {
		return ChatResult{}, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := c.newRequest(ctx, body)
	if err != nil {
		return ChatResult{}, err
	}

	resp, err := c.Client.Do(httpReq)
	if err != nil {
		return ChatResult{}, fmt.Errorf("messages request: %w", err)
	}
	defer resp.Body.Close()

	var parsed anthropicResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return ChatResult{}, fmt.Errorf("decode response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		if parsed.Error != nil {
			return ChatResult{}, fmt.Errorf("anthropic API error: %s", parsed.Error.Message)
		}
		return ChatResult{}, fmt.Errorf("anthropic API returned status %d", resp.StatusCode)
	}

	var text string
	var calls []ToolCall
	for _, block := range parsed.Content {
		switch block.Type {
		case "text":
			text += block.Text
		case "tool_use":
			calls = append(calls, ToolCall{ID: block.ID, Name: block.Name, Arguments: block.Input})
		}
	}
	// stop_reason == "max_tokens" means defaultMaxTokens cut this reply off
	// mid-generation — logged distinctly here (rather than left to surface
	// downstream as an opaque "tool call could not be validated" once its
	// truncated JSON fails to unmarshal) so the real cause is visible in the
	// server log the moment it happens, not reverse-engineered later. See
	// ChatResult.Truncated's doc comment.
	truncated := parsed.StopReason == "max_tokens"
	if truncated {
		log.Printf("anthropic: reply truncated by max_tokens cap (%d) — stop_reason=%q", defaultMaxTokens, parsed.StopReason)
	}
	return ChatResult{Content: text, ToolCalls: calls, TokensIn: parsed.Usage.InputTokens, TokensOut: parsed.Usage.OutputTokens, Truncated: truncated}, nil
}

type anthropicStreamEvent struct {
	Type         string `json:"type"`
	Index        int    `json:"index"`
	ContentBlock struct {
		Type string `json:"type"` // "text" or "tool_use" — only set on content_block_start
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"content_block"`
	Delta struct {
		Type string `json:"type"` // "text_delta" or "input_json_delta"
		Text string `json:"text"`
		// PartialJSON is one fragment of a tool_use block's "input" object,
		// streamed as raw (not necessarily individually valid) JSON text —
		// see the accumulation loop in ChatStream below.
		PartialJSON string `json:"partial_json"`
		// StopReason is only present on a "message_delta" event's delta
		// object (a differently-shaped object than the text/input_json
		// delta above, but decoded into this same struct — unset fields
		// simply stay zero-valued on events that don't carry them). See
		// anthropicResponse.StopReason's doc comment for what "max_tokens"
		// means here.
		StopReason string `json:"stop_reason"`
	} `json:"delta"`
	Usage   anthropicUsage `json:"usage"`
	Message struct {
		Usage anthropicUsage `json:"usage"`
	} `json:"message"`
}

func (c *AnthropicClient) ChatStream(ctx context.Context, req ChatRequest, onDelta func(string)) (ChatResult, error) {
	system, messages := splitSystem(req.Messages)
	body, err := json.Marshal(anthropicRequest{Model: req.Model, System: system, MaxTokens: defaultMaxTokens, Messages: messages, Stream: true, Tools: toAnthropicTools(req.Tools)})
	if err != nil {
		return ChatResult{}, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := c.newRequest(ctx, body)
	if err != nil {
		return ChatResult{}, err
	}

	resp, err := c.Client.Do(httpReq)
	if err != nil {
		return ChatResult{}, fmt.Errorf("messages request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var parsed anthropicResponse
		json.NewDecoder(resp.Body).Decode(&parsed)
		if parsed.Error != nil {
			return ChatResult{}, fmt.Errorf("anthropic API error: %s", parsed.Error.Message)
		}
		return ChatResult{}, fmt.Errorf("anthropic API returned status %d", resp.StatusCode)
	}

	var result ChatResult
	// pendingCalls/argBuf accumulate a tool_use block across its
	// content_block_start (ID/Name arrive here) and one-or-more
	// content_block_delta input_json_delta events (Input arrives in
	// fragments — Anthropic streams a tool call's arguments the same
	// incremental way it streams text), keyed by the block's Index so
	// multiple tool_use blocks in one response never interleave into each
	// other's buffers. Finalized into a ToolCall on that block's
	// content_block_stop.
	pendingCalls := map[int]*ToolCall{}
	argBuf := map[int]*strings.Builder{}
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var evt anthropicStreamEvent
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &evt); err != nil {
			continue
		}
		switch evt.Type {
		case "content_block_start":
			if evt.ContentBlock.Type == "tool_use" {
				pendingCalls[evt.Index] = &ToolCall{ID: evt.ContentBlock.ID, Name: evt.ContentBlock.Name}
				argBuf[evt.Index] = &strings.Builder{}
			}
		case "content_block_delta":
			switch evt.Delta.Type {
			case "input_json_delta":
				if buf, ok := argBuf[evt.Index]; ok {
					buf.WriteString(evt.Delta.PartialJSON)
				}
			default:
				if evt.Delta.Text != "" {
					result.Content += evt.Delta.Text
					onDelta(evt.Delta.Text)
				}
			}
		case "content_block_stop":
			if call, ok := pendingCalls[evt.Index]; ok {
				call.Arguments = json.RawMessage(argBuf[evt.Index].String())
				result.ToolCalls = append(result.ToolCalls, *call)
				delete(pendingCalls, evt.Index)
				delete(argBuf, evt.Index)
			}
		case "message_start":
			result.TokensIn = evt.Message.Usage.InputTokens
		case "message_delta":
			result.TokensOut = evt.Usage.OutputTokens
			// See Chat's identical check above — the streaming counterpart
			// arrives here instead of a top-level response field, on the
			// final message_delta event.
			if evt.Delta.StopReason == "max_tokens" {
				result.Truncated = true
				log.Printf("anthropic: streamed reply truncated by max_tokens cap (%d) — stop_reason=%q", defaultMaxTokens, evt.Delta.StopReason)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return result, fmt.Errorf("read stream: %w", err)
	}
	return result, nil
}
