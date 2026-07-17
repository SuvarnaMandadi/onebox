package embeddings

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// OllamaProvider calls a local Ollama daemon's embeddings endpoint. Ollama
// embeds one prompt per request, so Embed loops over texts sequentially —
// simple and broadly compatible across Ollama versions, at the cost of
// batch throughput (a fine v0.1 trade-off at the scale onebox targets).
type OllamaProvider struct {
	BaseURL string
	Model   string
	Client  *http.Client
}

func NewOllamaProvider(baseURL, model string) *OllamaProvider {
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	return &OllamaProvider{BaseURL: baseURL, Model: model, Client: http.DefaultClient}
}

type ollamaEmbedRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

type ollamaEmbedResponse struct {
	Embedding []float32 `json:"embedding"`
	Error     string    `json:"error"`
}

func (p *OllamaProvider) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, text := range texts {
		vec, err := p.embedOne(ctx, text)
		if err != nil {
			return nil, fmt.Errorf("embed text %d: %w", i, err)
		}
		out[i] = vec
	}
	return out, nil
}

func (p *OllamaProvider) embedOne(ctx context.Context, text string) ([]float32, error) {
	body, err := json.Marshal(ollamaEmbedRequest{Model: p.Model, Prompt: text})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.BaseURL+"/api/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embeddings request: %w", err)
	}
	defer resp.Body.Close()

	var parsed ollamaEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		if parsed.Error != "" {
			return nil, fmt.Errorf("ollama error: %s", parsed.Error)
		}
		return nil, fmt.Errorf("ollama returned status %d", resp.StatusCode)
	}
	return parsed.Embedding, nil
}
