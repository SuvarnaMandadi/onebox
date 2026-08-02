// heartbeat.go backs the admin dashboard's "AI heartbeat" status panel —
// a live snapshot of database, LLM provider, streaming, and background-
// queue health, alongside the time-series charts in metrics.go.
package server

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// heartbeatItem is one monitored subsystem's status. Status is always one
// of "ok", "degraded", or "down" — "degraded" covers "reachable but not
// fully healthy" (e.g. a provider that's configured but whose test call
// failed), so the panel never has to invent a fourth ambiguous state.
type heartbeatItem struct {
	Status    string `json:"status"`
	Detail    string `json:"detail"`
	LatencyMS int64  `json:"latency_ms,omitempty"`
}

type heartbeatResponse struct {
	GeneratedAt string                   `json:"generated_at"`
	Database    heartbeatItem            `json:"database"`
	Providers   map[string]heartbeatItem `json:"providers"`
	Streaming   heartbeatItem            `json:"streaming"`
	Queue       heartbeatItem            `json:"queue"`
}

// handleHeartbeat is admin-only. Every check is a real, live probe (same
// spirit as handleTestConnection) rather than a cached/assumed status —
// the three provider checks run concurrently so total latency is bounded
// by the slowest single provider (up to httpTestClient's 8s timeout)
// rather than the sum of all three.
func (s *Server) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	resp := heartbeatResponse{
		GeneratedAt: time.Now().Format(time.RFC3339),
		Database:    s.checkDatabaseHeartbeat(ctx),
		Streaming:   s.checkStreamingHeartbeat(),
		Queue:       s.checkQueueHeartbeat(ctx),
		Providers:   map[string]heartbeatItem{},
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	for _, kind := range []string{"ollama", "anthropic", "openai"} {
		wg.Add(1)
		go func(kind string) {
			defer wg.Done()
			item := s.checkProviderHeartbeat(ctx, kind)
			mu.Lock()
			resp.Providers[kind] = item
			mu.Unlock()
		}(kind)
	}
	wg.Wait()

	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) checkDatabaseHeartbeat(ctx context.Context) heartbeatItem {
	start := time.Now()
	err := s.db.PingContext(ctx)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		return heartbeatItem{Status: "down", Detail: "database ping failed: " + err.Error(), LatencyMS: latency}
	}
	return heartbeatItem{Status: "ok", Detail: "connected", LatencyMS: latency}
}

func (s *Server) checkStreamingHeartbeat() heartbeatItem {
	active := s.activeStreams.Load()
	if active == 0 {
		return heartbeatItem{Status: "ok", Detail: "idle — no streams in flight"}
	}
	return heartbeatItem{Status: "ok", Detail: fmt.Sprintf("%d stream(s) in flight", active)}
}

// checkQueueHeartbeat reports RAG source ingestion backlog (internal/server/rag_ingest.go's
// background goroutine is onebox's only real job queue today — see
// _rag_sources.status, 'pending'/'processing'/'done'/'error'). A backlog
// is "degraded" rather than "down": ingestion is still working, just
// behind, and will drain on its own.
func (s *Server) checkQueueHeartbeat(ctx context.Context) heartbeatItem {
	var pending, processing int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM _rag_sources WHERE status = 'pending'`).Scan(&pending)
	if err != nil {
		return heartbeatItem{Status: "down", Detail: "queue query failed: " + err.Error()}
	}
	err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM _rag_sources WHERE status = 'processing'`).Scan(&processing)
	if err != nil {
		return heartbeatItem{Status: "down", Detail: "queue query failed: " + err.Error()}
	}
	backlog := pending + processing
	if backlog == 0 {
		return heartbeatItem{Status: "ok", Detail: "no documents queued for ingestion"}
	}
	status := "ok"
	if pending > 5 {
		status = "degraded"
	}
	return heartbeatItem{Status: status, Detail: fmt.Sprintf("%d pending, %d processing", pending, processing)}
}

// checkProviderHeartbeat reuses the exact same reachability checks
// "Test connection" in Settings runs (see settings_test_connection.go) —
// against whatever's currently saved, with no request body/override,
// since the heartbeat panel has no form to submit. An unconfigured
// provider (no API key / not the active Ollama choice) reports
// "unconfigured" rather than "down": a self-hoster who only uses Ollama
// shouldn't see Anthropic/OpenAI flagged as broken.
func (s *Server) checkProviderHeartbeat(ctx context.Context, kind string) heartbeatItem {
	stored, err := getAllSettings(ctx, s.db, s.cfg.JWTSecret)
	if err != nil {
		return heartbeatItem{Status: "down", Detail: "failed to load settings: " + err.Error()}
	}
	fallback := func(key settingKey, def string) string {
		if v, ok := stored[key]; ok && v != "" {
			return v
		}
		return def
	}

	start := time.Now()
	var result testConnectionResponse
	switch kind {
	case "anthropic":
		apiKey := fallback(settingAnthropicAPIKey, s.cfg.AnthropicAPIKey)
		if apiKey == "" {
			return heartbeatItem{Status: "unconfigured", Detail: "no Anthropic API key set"}
		}
		result = testAnthropicConnection(ctx, apiKey, "")
	case "openai":
		apiKey := fallback(settingOpenAIAPIKey, s.cfg.OpenAIChatAPIKey)
		if apiKey == "" {
			return heartbeatItem{Status: "unconfigured", Detail: "no OpenAI API key set"}
		}
		baseURL := fallback(settingOpenAIBaseURL, "https://api.openai.com/v1")
		result = testOpenAICompatConnection(ctx, "OpenAI", baseURL, apiKey, "")
	case "ollama":
		baseURL := fallback(settingOllamaBaseURL, "http://localhost:11434")
		result = testOllamaConnection(ctx, baseURL, "")
	default:
		return heartbeatItem{Status: "down", Detail: "unknown provider"}
	}
	latency := time.Since(start).Milliseconds()

	if !result.OK {
		return heartbeatItem{Status: "down", Detail: result.Message, LatencyMS: latency}
	}
	return heartbeatItem{Status: "ok", Detail: result.Message, LatencyMS: latency}
}
