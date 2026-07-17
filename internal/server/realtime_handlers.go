package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"onebox/internal/auth"
)

const realtimePingInterval = 25 * time.Second

// realtimeAuth accepts a Bearer header like the other auth middleware, but
// also falls back to a ?token= query param: the browser EventSource API
// cannot set custom headers, so SSE clients have no other way to
// authenticate a GET request.
func (s *Server) realtimeAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenStr := bearerToken(r)
		if tokenStr == "" {
			tokenStr = r.URL.Query().Get("token")
		}
		if tokenStr != "" {
			if claims, err := auth.ParseToken(s.cfg.JWTSecret, tokenStr); err == nil {
				switch claims.Type {
				case auth.SubjectUser:
					r = r.WithContext(context.WithValue(r.Context(), ctxKeyAuthUserID, claims.Subject))
				case auth.SubjectAdmin:
					r = r.WithContext(context.WithValue(r.Context(), ctxKeyAuthAdminID, claims.Subject))
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

// handleRealtime streams record-change events to the caller over SSE until
// the client disconnects. Each connected client only receives events for
// records its own auth (public/authenticated/owner, or admin) would allow
// it to view.
func (s *Server) handleRealtime(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming_unsupported", "server does not support streaming", nil)
		return
	}

	isAdmin := false
	if _, ok := authAdminID(r.Context()); ok {
		isAdmin = true
	}
	userID, _ := authUserID(r.Context())

	client := s.hub.subscribe(isAdmin, userID)
	defer s.hub.unsubscribe(client)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ticker := time.NewTicker(realtimePingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case evt, ok := <-client.ch:
			if !ok {
				return
			}
			data, err := json.Marshal(evt)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: record_change\ndata: %s\n\n", data)
			flusher.Flush()
		case <-ticker.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}
