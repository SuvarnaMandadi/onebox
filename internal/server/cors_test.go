package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestCORSAllowsCrossOriginRequests exercises the exact scenario onebox
// exists for: a frontend on a different origin (its own dev server, a
// static site, a mobile webview) calling the API from a browser. Without
// CORS headers, the browser blocks the response before JS ever sees it —
// this would silently break every example app and every real app built
// with the SDK from a separate origin.
func TestCORSAllowsCrossOriginRequests(t *testing.T) {
	srv, _ := newTestServer(t)

	t.Run("preflight request is allowed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodOptions, "/api/auth/login", nil)
		req.Header.Set("Origin", "http://localhost:5173")
		req.Header.Set("Access-Control-Request-Method", "POST")
		req.Header.Set("Access-Control-Request-Headers", "Authorization, Content-Type")
		rec := httptest.NewRecorder()
		srv.Router().ServeHTTP(rec, req)

		if rec.Code != http.StatusOK && rec.Code != http.StatusNoContent {
			t.Fatalf("preflight status = %d, want 200 or 204, body = %s", rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" && got != "*" {
			t.Fatalf("Access-Control-Allow-Origin = %q, want the request origin or *", got)
		}
	})

	t.Run("actual request echoes the origin header", func(t *testing.T) {
		req := jsonRequest(t, http.MethodPost, "/api/auth/login", authRequest{Email: "nobody@example.com", Password: "x"})
		req.Header.Set("Origin", "http://localhost:5173")
		rec := httptest.NewRecorder()
		srv.Router().ServeHTTP(rec, req)

		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" && got != "*" {
			t.Fatalf("Access-Control-Allow-Origin = %q, want the request origin or *", got)
		}
	})
}
