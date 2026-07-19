package server

import (
	"net/http"
	"strconv"
)

// handleListLogs is admin-only: powers the dashboard's Logs page.
func (s *Server) handleListLogs(w http.ResponseWriter, r *http.Request) {
	status := 0
	if raw := r.URL.Query().Get("status"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			status = n
		}
	}
	path := r.URL.Query().Get("path")

	entries, err := listLogs(r.Context(), s.db, status, path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to list logs", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": entries})
}
