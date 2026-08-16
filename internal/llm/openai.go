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

// OpenAIClient calls an OpenAI-compatible /chat/completions endpoint.
type OpenAIClient struct {
	BaseURL string
	APIKey  string
	Client  *http.Client
}

func NewOpenAIClient(baseURL, apiKey string) *OpenAIClient {
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	return &OpenAIClient{BaseURL: baseURL, APIKey: apiKey, Client: http.DefaultClient}
}

type openAIChatRequest struct {
	Model    string          `json:"model"`
	Messages []openAIMessage `json:"messages"`
	Stream   bool            `json:"stream,omitempty"`
	Tools    []openAITool    `json:"tools,omitempty"`
	// MaxTokens is omitted entirely (via omitempty, letting the API apply
	// its own default) unless the caller set ChatRequest.MaxTokens
	// explicitly. Every pre-existing caller leaves MaxTokens at 0, so this
	// field being new changes nothing for them.
	MaxTokens int `json:"max_tokens,omitempty"`
}

// openAITool is OpenAI's wire shape for one offered tool: a nested
// {type:"function", function:{name, description, parameters}} object —
// unlike Anthropic's flat {name, description, input_schema}. Ollama's
// /api/chat tool format is identical to this one, so ollama.go reuses the
// same shape rather than duplicating it (see ollamaTool).
type openAITool struct {
	Type     string             `json:"type"` // always "function"
	Function openAIToolFunction `json:"function"`
}

type openAIToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

// toOpenAITools converts the provider-agnostic Tool list to OpenAI's wire
// shape. Returns nil (omitted via omitempty above) for the common
// no-tools case, so a plain chat request's body is byte-for-byte
// unchanged from before tool-calling existed.
func toOpenAITools(tools []Tool) []openAITool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]openAITool, len(tools))
	for i, t := range tools {
		out[i] = openAITool{Type: "function", Function: openAIToolFunction{Name: t.Name, Description: t.Description, Parameters: t.Schema}}
	}
	return out
}

// openAIMessage is OpenAI's wire shape for a chat message. Content is
// `any` because OpenAI accepts either a plain string (ordinary text-only
// messages — the shape every message used before image support existed)
// or an array of typed parts (required the moment a message carries an
// image). See toOpenAIMessages.
type openAIMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
	// ToolCalls, set only on a role:"assistant" message being replayed as
	// history after a tool-execution round (see toOpenAIMessages), is
	// OpenAI's native tool_calls field — required so the following
	// role:"tool" message's ToolCallID has a matching call to attach to.
	ToolCalls []openAIRequestToolCall `json:"tool_calls,omitempty"`
	// ToolCallID, set only on a role:"tool" message, is OpenAI's native
	// tool_call_id field linking this result back to one entry in the
	// immediately preceding assistant message's ToolCalls.
	ToolCallID string `json:"tool_call_id,omitempty"`
}

// openAIRequestToolCall is OpenAI's wire shape for one tool call inside an
// outgoing assistant-message's tool_calls array — distinct from
// openAIToolCallWire (the shape a *response* arrives in) only in that this
// one always carries Type:"function", matching what OpenAI's API requires
// on replay. See toOpenAIRequestToolCalls.
type openAIRequestToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"` // always "function"
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// toOpenAIRequestToolCalls converts the provider-agnostic ToolCall list
// (from Message.ToolCalls) into OpenAI's outgoing wire shape.
func toOpenAIRequestToolCalls(calls []ToolCall) []openAIRequestToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]openAIRequestToolCall, len(calls))
	for i, c := range calls {
		out[i].ID = c.ID
		out[i].Type = "function"
		out[i].Function.Name = c.Name
		out[i].Function.Arguments = string(c.Arguments)
	}
	return out
}

type openAIContentPart struct {
	Type     string          `json:"type"` // "text" or "image_url"
	Text     string          `json:"text,omitempty"`
	ImageURL *openAIImageURL `json:"image_url,omitempty"`
}

type openAIImageURL struct {
	URL string `json:"url"`
}

// toOpenAIMessages converts the provider-agnostic Message list to OpenAI's
// wire shape. A message with no images keeps the plain-string Content
// shape (byte-for-byte identical to before image support existed); a
// message with images becomes a content-part array — a leading text part
// (if Content is non-empty) followed by one image_url part per attachment,
// each a full "data:<mime>;base64,..." URI, since OpenAI (unlike Ollama)
// has no separate flat images field.
func toOpenAIMessages(messages []Message) []openAIMessage {
	out := make([]openAIMessage, len(messages))
	for i, m := range messages {
		// Role:"tool" carries a tool's result back to the model — OpenAI's
		// native shape for this is {role:"tool", tool_call_id, content},
		// never an image/text content-part array.
		if m.Role == "tool" {
			out[i] = openAIMessage{Role: "tool", Content: m.Content, ToolCallID: m.ToolCallID}
			continue
		}
		// An assistant turn being replayed as history after making tool
		// calls: Content may be empty ("" is fine — OpenAI-compatible APIs
		// accept an empty string alongside tool_calls) plus the native
		// tool_calls array so the following tool-result message(s) have a
		// matching call to attach to.
		if len(m.ToolCalls) > 0 {
			out[i] = openAIMessage{Role: m.Role, Content: m.Content, ToolCalls: toOpenAIRequestToolCalls(m.ToolCalls)}
			continue
		}
		if len(m.Images) == 0 {
			out[i] = openAIMessage{Role: m.Role, Content: m.Content}
			continue
		}
		parts := make([]openAIContentPart, 0, len(m.Images)+1)
		if m.Content != "" {
			parts = append(parts, openAIContentPart{Type: "text", Text: m.Content})
		}
		for _, img := range m.Images {
			uri := "data:" + img.MediaType + ";base64," + base64.StdEncoding.EncodeToString(img.Data)
			parts = append(parts, openAIContentPart{Type: "image_url", ImageURL: &openAIImageURL{URL: uri}})
		}
		out[i] = openAIMessage{Role: m.Role, Content: parts}
	}
	return out
}

type openAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

// openAIToolCallWire is OpenAI's wire shape for one tool call the model
// decided to make — Function.Arguments arrives as a JSON-encoded *string*
// (OpenAI always sends it this way, unlike Ollama's native-object
// arguments), so it's typed as a plain Go string here and converted to
// json.RawMessage(...) directly (no unwrapping needed — see toToolCalls).
type openAIToolCallWire struct {
	ID       string `json:"id"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type openAIChatResponse struct {
	Choices []openAIChoiceWire `json:"choices"`
	Usage   openAIUsage        `json:"usage"`
	Error   *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// openAIChoiceWire and openAIResponseMessageWire are named (rather than
// inline anonymous structs, as these used to be) so openai_test.go can
// construct one directly instead of re-declaring its exact field set at
// every call site.
type openAIChoiceWire struct {
	Message openAIResponseMessageWire `json:"message"`
}

type openAIResponseMessageWire struct {
	Content   string               `json:"content"`
	ToolCalls []openAIToolCallWire `json:"tool_calls,omitempty"`
}

// toToolCalls converts OpenAI's wire-shape tool calls to the
// provider-agnostic ToolCall list. Returns nil for the common no-tool-call
// case.
func toToolCalls(wire []openAIToolCallWire) []ToolCall {
	if len(wire) == 0 {
		return nil
	}
	out := make([]ToolCall, len(wire))
	for i, w := range wire {
		out[i] = ToolCall{ID: w.ID, Name: w.Function.Name, Arguments: json.RawMessage(w.Function.Arguments)}
	}
	return out
}

func (c *OpenAIClient) newRequest(ctx context.Context, body []byte) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	return req, nil
}

func (c *OpenAIClient) Chat(ctx context.Context, req ChatRequest) (ChatResult, error) {
	body, err := json.Marshal(openAIChatRequest{Model: req.Model, Messages: toOpenAIMessages(req.Messages), Tools: toOpenAITools(req.Tools), MaxTokens: req.MaxTokens})
	if err != nil {
		return ChatResult{}, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := c.newRequest(ctx, body)
	if err != nil {
		return ChatResult{}, err
	}

	resp, err := c.Client.Do(httpReq)
	if err != nil {
		return ChatResult{}, fmt.Errorf("chat completions request: %w", err)
	}
	defer resp.Body.Close()

	var parsed openAIChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return ChatResult{}, fmt.Errorf("decode response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		if parsed.Error != nil {
			return ChatResult{}, fmt.Errorf("openai API error: %s", parsed.Error.Message)
		}
		return ChatResult{}, fmt.Errorf("openai API returned status %d", resp.StatusCode)
	}
	if len(parsed.Choices) == 0 {
		return ChatResult{}, fmt.Errorf("openai API returned no choices")
	}

	return ChatResult{
		Content:   parsed.Choices[0].Message.Content,
		ToolCalls: toToolCalls(parsed.Choices[0].Message.ToolCalls),
		TokensIn:  parsed.Usage.PromptTokens,
		TokensOut: parsed.Usage.CompletionTokens,
	}, nil
}

// openAIStreamToolCallDelta is one fragment of a streamed tool call.
// Index identifies which tool call this fragment belongs to (a response
// can stream more than one call concurrently); ID and Function.Name only
// arrive on that index's first delta, while Function.Arguments arrives as
// successive string fragments to be concatenated — mirroring how content
// deltas work, just for the arguments string instead of prose.
type openAIStreamToolCallDelta struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type openAIStreamChunk struct {
	Choices []struct {
		Delta struct {
			Content   string                      `json:"content"`
			ToolCalls []openAIStreamToolCallDelta `json:"tool_calls,omitempty"`
		} `json:"delta"`
	} `json:"choices"`
	Usage *openAIUsage `json:"usage"`
}

func (c *OpenAIClient) ChatStream(ctx context.Context, req ChatRequest, onDelta func(string)) (ChatResult, error) {
	body, err := json.Marshal(openAIChatRequest{Model: req.Model, Messages: toOpenAIMessages(req.Messages), Stream: true, Tools: toOpenAITools(req.Tools), MaxTokens: req.MaxTokens})
	if err != nil {
		return ChatResult{}, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := c.newRequest(ctx, body)
	if err != nil {
		return ChatResult{}, err
	}

	resp, err := c.Client.Do(httpReq)
	if err != nil {
		return ChatResult{}, fmt.Errorf("chat completions request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var parsed openAIChatResponse
		json.NewDecoder(resp.Body).Decode(&parsed)
		if parsed.Error != nil {
			return ChatResult{}, fmt.Errorf("openai API error: %s", parsed.Error.Message)
		}
		return ChatResult{}, fmt.Errorf("openai API returned status %d", resp.StatusCode)
	}

	var result ChatResult
	// callsByIndex accumulates each streamed tool call's ID/Name/Arguments
	// fragments across chunks, keyed by the call's own Index (see
	// openAIStreamToolCallDelta) — OpenAI has no per-call "stop" event the
	// way Anthropic does, so calls are only finalized once the stream ends.
	callsByIndex := map[int]*ToolCall{}
	var callOrder []int
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			break
		}
		var chunk openAIStreamChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			continue
		}
		if chunk.Usage != nil {
			result.TokensIn = chunk.Usage.PromptTokens
			result.TokensOut = chunk.Usage.CompletionTokens
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.Content != "" {
				result.Content += choice.Delta.Content
				onDelta(choice.Delta.Content)
			}
			for _, tc := range choice.Delta.ToolCalls {
				call, seen := callsByIndex[tc.Index]
				if !seen {
					call = &ToolCall{}
					callsByIndex[tc.Index] = call
					callOrder = append(callOrder, tc.Index)
				}
				if tc.ID != "" {
					call.ID = tc.ID
				}
				if tc.Function.Name != "" {
					call.Name = tc.Function.Name
				}
				call.Arguments = append(call.Arguments, []byte(tc.Function.Arguments)...)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return result, fmt.Errorf("read stream: %w", err)
	}
	for _, idx := range callOrder {
		result.ToolCalls = append(result.ToolCalls, *callsByIndex[idx])
	}
	return result, nil
}
