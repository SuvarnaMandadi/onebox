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
