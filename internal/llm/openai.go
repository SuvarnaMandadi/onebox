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
}

// openAITool is Tool translated into OpenAI's function-calling shape —
// https://platform.openai.com/docs/guides/function-calling. Parameters
// takes the Tool's JSON Schema as-is, the same as every other provider
// here: a field rename, not a translation.
type openAITool struct {
	Type     string             `json:"type"` // always "function"
	Function openAIToolFunction `json:"function"`
}

type openAIToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

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

// openAIToolCall is a single entry of message.tool_calls in OpenAI's
// response — Arguments is a JSON-encoded *string* on the wire (unlike
// Anthropic/Ollama, which both send a real JSON object), so it needs an
// explicit re-typing to json.RawMessage rather than a field rename; see
// toToolCalls.
type openAIToolCall struct {
	ID       string `json:"id"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func toToolCalls(calls []openAIToolCall) []ToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]ToolCall, len(calls))
	for i, c := range calls {
		out[i] = ToolCall{ID: c.ID, Name: c.Function.Name, Arguments: json.RawMessage(c.Function.Arguments)}
	}
	return out
}

type openAIChatResponse struct {
	Choices []struct {
		Message struct {
			Content   string           `json:"content"`
			ToolCalls []openAIToolCall `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
	Usage openAIUsage `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
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
	body, err := json.Marshal(openAIChatRequest{Model: req.Model, Messages: toOpenAIMessages(req.Messages), Tools: toOpenAITools(req.Tools)})
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
		TokensIn:  parsed.Usage.PromptTokens,
		TokensOut: parsed.Usage.CompletionTokens,
		ToolCalls: toToolCalls(parsed.Choices[0].Message.ToolCalls),
	}, nil
}

// openAIStreamToolCall is one delta.tool_calls entry — Index correlates
// fragments across chunks the same way Anthropic's content-block Index
// does: OpenAI sends a tool call's id/name once (on the chunk that starts
// it) and its arguments as a run of string fragments after that, all
// sharing one Index, so nothing here is a complete ToolCall until the
// stream ends.
type openAIStreamToolCall struct {
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
			Content   string                 `json:"content"`
			ToolCalls []openAIStreamToolCall `json:"tool_calls"`
		} `json:"delta"`
	} `json:"choices"`
	Usage *openAIUsage `json:"usage"`
}

func (c *OpenAIClient) ChatStream(ctx context.Context, req ChatRequest, onDelta func(string)) (ChatResult, error) {
	body, err := json.Marshal(openAIChatRequest{Model: req.Model, Messages: toOpenAIMessages(req.Messages), Stream: true, Tools: toOpenAITools(req.Tools)})
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
	// toolCalls accumulates fragments by Index — see openAIStreamToolCall's
	// doc comment. toolOrder preserves first-seen order since Go maps
	// don't, so the finished ToolCalls slice comes out in the order the
	// model emitted them rather than random map iteration order.
	toolCalls := map[int]*ToolCall{}
	var toolOrder []int
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
			// Tool-call argument fragments are never forwarded to
			// onDelta — see ToolCall's doc comment in llm.go: they're
			// structured data for ToolCalls, not chat text, and aren't
			// valid JSON until every fragment has arrived.
			for _, tc := range choice.Delta.ToolCalls {
				existing, ok := toolCalls[tc.Index]
				if !ok {
					existing = &ToolCall{}
					toolCalls[tc.Index] = existing
					toolOrder = append(toolOrder, tc.Index)
				}
				if tc.ID != "" {
					existing.ID = tc.ID
				}
				if tc.Function.Name != "" {
					existing.Name += tc.Function.Name
				}
				existing.Arguments = json.RawMessage(string(existing.Arguments) + tc.Function.Arguments)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return result, fmt.Errorf("read stream: %w", err)
	}
	for _, idx := range toolOrder {
		result.ToolCalls = append(result.ToolCalls, *toolCalls[idx])
	}
	return result, nil
}
