package server

import (
	"encoding/json"
	"fmt"
	"net/http"

	"onebox/internal/llm"
)

type llmChatRequest struct {
	Model    string        `json:"model"`
	Messages []llm.Message `json:"messages"`
	Stream   bool          `json:"stream"`
}

type llmChatResponse struct {
	Content string `json:"content"`
	Cached  bool   `json:"cached"`
}

// handleLLMChat is the provider-agnostic /api/llm/chat gateway: caller
// sends {model, messages}, onebox routes to Anthropic/OpenAI/Ollama by
// model name prefix (see llm.ProviderKind), applies the response cache
// and per-user rate/spend limits, and logs usage.
func (s *Server) handleLLMChat(w http.ResponseWriter, r *http.Request) {
	var req llmChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Model == "" || len(req.Messages) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_body", `expected {"model": "...", "messages": [...]}`, nil)
		return
	}

	uid := billingID(r.Context())
	if !s.rateLimiter.Allow(uid) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many requests, slow down", nil)
		return
	}
	if s.cfg.MonthlySpendCapUSD > 0 {
		spend, err := monthlySpend(r.Context(), s.db, uid)
		if err == nil && spend >= s.cfg.MonthlySpendCapUSD {
			writeError(w, http.StatusPaymentRequired, "spend_limit_exceeded", "monthly spend cap reached", nil)
			return
		}
	}

	chatReq := llm.ChatRequest{Model: req.Model, Messages: req.Messages}
	provider := llm.ProviderKind(req.Model)

	if req.Stream {
		s.streamLLMChat(w, r, chatReq, provider)
		return
	}

	cacheKey := chatCacheKey(chatReq)
	if cached, ok := s.chatCache.Get(cacheKey); ok {
		s.logUsage(r.Context(), provider, req.Model, cached.TokensIn, cached.TokensOut, true)
		writeJSON(w, http.StatusOK, llmChatResponse{Content: cached.Content, Cached: true})
		return
	}

	result, err := s.llmRouter.Chat(r.Context(), chatReq)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), nil)
		return
	}
	s.chatCache.Set(cacheKey, result)
	s.logUsage(r.Context(), provider, req.Model, result.TokensIn, result.TokensOut, false)
	writeJSON(w, http.StatusOK, llmChatResponse{Content: result.Content, Cached: false})
}

// streamLLMChat bypasses the cache in both directions — a streamed
// response has no natural single "content" to cache a partial read
// against — and streams provider deltas out as SSE.
func (s *Server) streamLLMChat(w http.ResponseWriter, r *http.Request, req llm.ChatRequest, provider string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming_unsupported", "server does not support streaming", nil)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	result, err := s.llmRouter.ChatStream(r.Context(), req, func(delta string) {
		data, _ := json.Marshal(map[string]string{"delta": delta})
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	})
	if err != nil {
		data, _ := json.Marshal(map[string]string{"error": err.Error()})
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
		return
	}

	s.logUsage(r.Context(), provider, req.Model, result.TokensIn, result.TokensOut, false)
	fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
}

// handleUsage powers GET /api/usage: regular users see only their own
// usage; admins see everyone's, optionally filtered to one user_id.
func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request) {
	_, isAdmin := authAdminID(r.Context())
	uid, _ := authUserID(r.Context())

	filterUserID := uid
	if isAdmin {
		filterUserID = r.URL.Query().Get("user_id")
	}

	records, err := listUsage(r.Context(), s.db, filterUserID, r.URL.Query().Get("from"), r.URL.Query().Get("to"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to list usage", nil)
		return
	}

	var totalCost float64
	for _, rec := range records {
		totalCost += rec.CostEstimate
	}

	writeJSON(w, http.StatusOK, map[string]any{"items": records, "total_cost_estimate": totalCost})
}
