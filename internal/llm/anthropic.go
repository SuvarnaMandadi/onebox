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
	Type   string                `json:"type"` // "text" or "image"
	Text   string                `json:"text,omitempty"`
	Source *anthropicImageSource `json:"source,omitempty"`
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

// anthropicTool is Tool translated into Anthropic's Messages API shape —
// see https://docs.anthropic.com/en/docs/build-with-claude/tool-use.
// InputSchema takes the Tool's JSON Schema as-is (Anthropic's schema
// dialect is the same draft this codebase's Tool.Schema values are
// written in), so this is a field rename, not a translation.
type anthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

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
	Content []struct {
		Type  string          `json:"type"` // "text" or "tool_use"
		Text  string          `json:"text,omitempty"`
		ID    string          `json:"id,omitempty"`
		Name  string          `json:"name,omitempty"`
		Input json.RawMessage `json:"input,omitempty"`
	} `json:"content"`
	Usage anthropicUsage `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
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
		rest = append(rest, anthropicMessage{Role: m.Role, Content: anthropicContent(m)})
	}
	return system.String(), rest
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
	return ChatResult{Content: text, TokensIn: parsed.Usage.InputTokens, TokensOut: parsed.Usage.OutputTokens, ToolCalls: calls}, nil
}

type anthropicStreamEvent struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
	// ContentBlock is only present on a content_block_start event — it
	// announces what kind of block is starting at Index (and, for a
	// tool_use block, the call's id/name up front; its arguments arrive
	// afterward as a run of input_json_delta events on the same index).
	ContentBlock struct {
		Type string `json:"type"` // "text" or "tool_use"
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"content_block"`
	Delta struct {
		Type string `json:"type"` // "text_delta" or "input_json_delta"
		Text string `json:"text"`
		// PartialJSON is one fragment of a tool_use block's arguments —
		// concatenating every input_json_delta for a given Index yields
		// that call's complete, validly-parseable JSON object exactly
		// once content_block_stop fires for it.
		PartialJSON string `json:"partial_json"`
	} `json:"delta"`
	Usage   anthropicUsage `json:"usage"`
	Message struct {
		Usage anthropicUsage `json:"usage"`
	} `json:"message"`
}

// anthropicStreamBlock tracks one in-progress content block by its stream
// index between content_block_start and content_block_stop — Anthropic
// interleaves blocks by index rather than sending each one as a single
// atomic unit, so a tool call's name/id (known at content_block_start)
// and its arguments (assembled from possibly many input_json_delta
// fragments) have to be correlated across several events before they can
// become one ToolCall.
type anthropicStreamBlock struct {
	kind  string // "text" or "tool_use"
	id    string
	name  string
	input strings.Builder
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
	blocks := map[int]*anthropicStreamBlock{}
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
			blocks[evt.Index] = &anthropicStreamBlock{kind: evt.ContentBlock.Type, id: evt.ContentBlock.ID, name: evt.ContentBlock.Name}
		case "content_block_delta":
			switch evt.Delta.Type {
			case "text_delta":
				if evt.Delta.Text != "" {
					result.Content += evt.Delta.Text
					onDelta(evt.Delta.Text)
				}
			case "input_json_delta":
				// Never forwarded to onDelta — a tool call's arguments are
				// structured data for ToolCalls, not chat text, and only
				// become valid JSON once every fragment has arrived.
				if b := blocks[evt.Index]; b != nil {
					b.input.WriteString(evt.Delta.PartialJSON)
				}
			}
		case "content_block_stop":
			if b := blocks[evt.Index]; b != nil && b.kind == "tool_use" {
				args := b.input.String()
				if args == "" {
					args = "{}"
				}
				result.ToolCalls = append(result.ToolCalls, ToolCall{ID: b.id, Name: b.name, Arguments: json.RawMessage(args)})
			}
			delete(blocks, evt.Index)
		case "message_start":
			result.TokensIn = evt.Message.Usage.InputTokens
		case "message_delta":
			result.TokensOut = evt.Usage.OutputTokens
		}
	}
	if err := scanner.Err(); err != nil {
		return result, fmt.Errorf("read stream: %w", err)
	}
	return result, nil
}
