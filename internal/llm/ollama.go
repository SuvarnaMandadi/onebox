package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Stream   bool      `json:"stream"`
}

type ollamaChatChunk struct {
	Message struct {
		Content string `json:"content"`
	} `json:"message"`
	Done            bool   `json:"done"`
	PromptEvalCount int    `json:"prompt_eval_count"`
	EvalCount       int    `json:"eval_count"`
	Error           string `json:"error"`
}

func (c *OllamaClient) newRequest(ctx context.Context, body []byte) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

func (c *OllamaClient) Chat(ctx context.Context, req ChatRequest) (ChatResult, error) {
	body, err := json.Marshal(ollamaChatRequest{Model: req.Model, Messages: req.Messages, Stream: false})
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

	var parsed ollamaChatChunk
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return ChatResult{}, fmt.Errorf("decode response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		if parsed.Error != "" {
			return ChatResult{}, fmt.Errorf("ollama error: %s", parsed.Error)
		}
		return ChatResult{}, fmt.Errorf("ollama returned status %d", resp.StatusCode)
	}

	return ChatResult{
		Content:   parsed.Message.Content,
		TokensIn:  parsed.PromptEvalCount,
		TokensOut: parsed.EvalCount,
	}, nil
}

func (c *OllamaClient) ChatStream(ctx context.Context, req ChatRequest, onDelta func(string)) (ChatResult, error) {
	body, err := json.Marshal(ollamaChatRequest{Model: req.Model, Messages: req.Messages, Stream: true})
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
