package llm

import (
	"bufio"
	"bytes"
	"context"
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

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicRequest struct {
	Model     string             `json:"model"`
	System    string             `json:"system,omitempty"`
	MaxTokens int                `json:"max_tokens"`
	Messages  []anthropicMessage `json:"messages"`
	Stream    bool               `json:"stream,omitempty"`
}

type anthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type anthropicResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
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
		rest = append(rest, anthropicMessage{Role: m.Role, Content: m.Content})
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
	body, err := json.Marshal(anthropicRequest{Model: req.Model, System: system, MaxTokens: defaultMaxTokens, Messages: messages})
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
	for _, block := range parsed.Content {
		if block.Type == "text" {
			text += block.Text
		}
	}
	return ChatResult{Content: text, TokensIn: parsed.Usage.InputTokens, TokensOut: parsed.Usage.OutputTokens}, nil
}

type anthropicStreamEvent struct {
	Type  string `json:"type"`
	Delta struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"delta"`
	Usage   anthropicUsage `json:"usage"`
	Message struct {
		Usage anthropicUsage `json:"usage"`
	} `json:"message"`
}

func (c *AnthropicClient) ChatStream(ctx context.Context, req ChatRequest, onDelta func(string)) (ChatResult, error) {
	system, messages := splitSystem(req.Messages)
	body, err := json.Marshal(anthropicRequest{Model: req.Model, System: system, MaxTokens: defaultMaxTokens, Messages: messages, Stream: true})
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
		case "content_block_delta":
			if evt.Delta.Text != "" {
				result.Content += evt.Delta.Text
				onDelta(evt.Delta.Text)
			}
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
