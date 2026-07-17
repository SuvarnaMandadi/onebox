package server

import "net/http"

type healthResponse struct {
	Status string `json:"status"`
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if err := s.db.PingContext(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, "db_unavailable", "database ping failed", nil)
		return
	}
	writeJSON(w, http.StatusOK, healthResponse{Status: "ok"})
}
