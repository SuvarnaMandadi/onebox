package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

const defaultMaxTokens = 1024

// AnthropicClient calls the Anthropic Messages API.
type AnthropicClient struct {
	BaseURL string
	APIKey  string
	Model   string
	Client  *http.Client
}

func NewAnthropicClient(baseURL, apiKey, model string) *AnthropicClient {
	if baseURL == "" {
		baseURL = "https://api.anthropic.com"
	}
	return &AnthropicClient{BaseURL: baseURL, APIKey: apiKey, Model: model, Client: http.DefaultClient}
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
}

type anthropicResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (c *AnthropicClient) Complete(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	reqBody, err := json.Marshal(anthropicRequest{
		Model:     c.Model,
		System:    systemPrompt,
		MaxTokens: defaultMaxTokens,
		Messages:  []anthropicMessage{{Role: "user", Content: userPrompt}},
	})
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v1/messages", bytes.NewReader(reqBody))
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := c.Client.Do(req)
	if err != nil {
		return "", fmt.Errorf("messages request: %w", err)
	}
	defer resp.Body.Close()

	var parsed anthropicResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		if parsed.Error != nil {
			return "", fmt.Errorf("anthropic API error: %s", parsed.Error.Message)
		}
		return "", fmt.Errorf("anthropic API returned status %d", resp.StatusCode)
	}

	var text string
	for _, block := range parsed.Content {
		if block.Type == "text" {
			text += block.Text
		}
	}
	return text, nil
}
