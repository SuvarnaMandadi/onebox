package server

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func TestListLogsRecordsRequestsAndFilters(t *testing.T) {
	srv, _ := newTestServer(t)
	adminToken := bootstrapAdmin(t, srv)

	// A couple of requests to generate log rows, including a 404.
	doAuth(t, srv, http.MethodGet, "/api/health", "", nil)
	doAuth(t, srv, http.MethodGet, "/api/does-not-exist", "", nil)

	var items []logEntry
	for i := 0; i < 20; i++ {
		rec := doAuth(t, srv, http.MethodGet, "/api/logs", adminToken, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
		var resp struct {
			Items []logEntry `json:"items"`
		}
		json.Unmarshal(rec.Body.Bytes(), &resp)
		items = resp.Items
		if len(items) >= 3 { // health + does-not-exist + this /api/logs call itself
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(items) < 3 {
		t.Fatalf("expected at least 3 logged requests, got %d: %+v", len(items), items)
	}

	t.Run("non-admin rejected", func(t *testing.T) {
		_, userToken := signupUser(t, srv, "notadmin@example.com")
		rec := doAuth(t, srv, http.MethodGet, "/api/logs", userToken, nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
	})

	t.Run("filter by status", func(t *testing.T) {
		rec := doAuth(t, srv, http.MethodGet, "/api/logs?status=404", adminToken, nil)
		var resp struct {
			Items []logEntry `json:"items"`
		}
		json.Unmarshal(rec.Body.Bytes(), &resp)
		for _, e := range resp.Items {
			if e.Status != 404 {
				t.Fatalf("filter status=404 returned a %d row", e.Status)
			}
		}
		if len(resp.Items) == 0 {
			t.Fatalf("expected at least one 404 row")
		}
	})

	t.Run("filter by path", func(t *testing.T) {
		rec := doAuth(t, srv, http.MethodGet, "/api/logs?path=health", adminToken, nil)
		var resp struct {
			Items []logEntry `json:"items"`
		}
		json.Unmarshal(rec.Body.Bytes(), &resp)
		if len(resp.Items) == 0 {
			t.Fatalf("expected at least one row matching path=health")
		}
	})
}

// TestLogsMetricsReturnsRequestSeriesAndProcessStats is the end-to-end
// regression test for the Logs page's rebuild into a monitoring dashboard
// (see metrics.go) — pins that /api/logs/metrics is admin-only, returns a
// dense (no-gap) per-minute series covering the full window, and includes
// live process stats.
func TestLogsMetricsReturnsRequestSeriesAndProcessStats(t *testing.T) {
	srv, _ := newTestServer(t)
	adminToken := bootstrapAdmin(t, srv)

	// Generate a couple of logged requests (async insert — see
	// TestListLogsRecordsRequestsAndFilters above for why this needs a
	// retry-poll rather than a fixed sleep).
	doAuth(t, srv, http.MethodGet, "/api/health", "", nil)
	doAuth(t, srv, http.MethodGet, "/api/does-not-exist", "", nil)

	t.Run("non-admin rejected", func(t *testing.T) {
		_, userToken := signupUser(t, srv, "notadmin-metrics@example.com")
		rec := doAuth(t, srv, http.MethodGet, "/api/logs/metrics", userToken, nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
	})

	var resp dashboardMetricsResponse
	for i := 0; i < 20; i++ {
		rec := doAuth(t, srv, http.MethodGet, "/api/logs/metrics", adminToken, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
		json.Unmarshal(rec.Body.Bytes(), &resp)
		if resp.TotalRequestsWindow >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
		resp = dashboardMetricsResponse{}
	}
	if resp.TotalRequestsWindow < 2 {
		t.Fatalf("expected at least 2 requests counted in the window, got %d", resp.TotalRequestsWindow)
	}
	if resp.WindowMinutes != dashboardMetricsWindowMinutes {
		t.Fatalf("window_minutes = %d, want %d", resp.WindowMinutes, dashboardMetricsWindowMinutes)
	}
	wantPoints := dashboardMetricsWindowMinutes + 1
	for name, series := range map[string][]timeBucket{
		"requests_per_minute":        resp.RequestsPerMinute,
		"avg_response_ms_per_minute": resp.AvgResponseMSPerMinute,
		"errors_per_minute":          resp.ErrorsPerMinute,
		"tokens_per_minute":          resp.TokensPerMinute,
	} {
		if len(series) != wantPoints {
			t.Errorf("%s has %d points, want %d (dense, no gaps)", name, len(series), wantPoints)
		}
	}
	if resp.TotalErrorsWindow < 1 {
		t.Errorf("expected at least 1 error counted (the /api/does-not-exist 404), got %d", resp.TotalErrorsWindow)
	}
	if resp.Process.Goroutines <= 0 {
		t.Errorf("process.goroutines = %d, want > 0", resp.Process.Goroutines)
	}
	if resp.Process.UptimeSeconds < 0 {
		t.Errorf("process.uptime_seconds = %v, want >= 0", resp.Process.UptimeSeconds)
	}
}

// TestHeartbeatReportsSubsystems pins /api/heartbeat's shape: admin-only,
// reports database/streaming/queue directly and all three providers by
// name (ollama/anthropic/openai) regardless of whether they're configured
// — an unconfigured provider must report "unconfigured", never "down"
// (which would misleadingly suggest something is broken rather than
// simply unset).
func TestHeartbeatReportsSubsystems(t *testing.T) {
	srv, _ := newTestServer(t)
	adminToken := bootstrapAdmin(t, srv)

	t.Run("non-admin rejected", func(t *testing.T) {
		_, userToken := signupUser(t, srv, "notadmin-heartbeat@example.com")
		rec := doAuth(t, srv, http.MethodGet, "/api/heartbeat", userToken, nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
	})

	rec := doAuth(t, srv, http.MethodGet, "/api/heartbeat", adminToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp heartbeatResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)

	if resp.Database.Status != "ok" {
		t.Errorf("database.status = %q, want ok (test server uses a real in-process SQLite)", resp.Database.Status)
	}
	if resp.Streaming.Status != "ok" {
		t.Errorf("streaming.status = %q, want ok (no streams in flight)", resp.Streaming.Status)
	}
	if resp.Queue.Status != "ok" {
		t.Errorf("queue.status = %q, want ok (no RAG sources ingested)", resp.Queue.Status)
	}
	validStatus := map[string]bool{"ok": true, "degraded": true, "down": true, "unconfigured": true}
	for _, kind := range []string{"ollama", "anthropic", "openai"} {
		item, ok := resp.Providers[kind]
		if !ok {
			t.Fatalf("providers map missing %q", kind)
		}
		if !validStatus[item.Status] {
			t.Errorf("providers[%q].status = %q, not a recognized status", kind, item.Status)
		}
	}
	// A fresh test server has no API keys configured for either hosted
	// provider — these two are deterministic regardless of environment
	// (unlike Ollama, whose status depends on whether something happens
	// to be listening on the default local port).
	if resp.Providers["anthropic"].Status != "unconfigured" {
		t.Errorf("providers[anthropic].status = %q, want unconfigured (no API key set)", resp.Providers["anthropic"].Status)
	}
	if resp.Providers["openai"].Status != "unconfigured" {
		t.Errorf("providers[openai].status = %q, want unconfigured (no API key set)", resp.Providers["openai"].Status)
	}
}
