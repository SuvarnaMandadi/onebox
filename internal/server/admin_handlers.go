package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"onebox/internal/auth"
)

type adminAuthResponse struct {
	Token  string `json:"token"`
	Record *admin `json:"record"`
}

// handleAdminSignup creates the first dashboard administrator. Once at
// least one admin exists, this endpoint is closed — further admins must be
// created from the dashboard by an existing admin (post-v0.1).
func (s *Server) handleAdminSignup(w http.ResponseWriter, r *http.Request) {
	count, err := countAdmins(r.Context(), s.db)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to check admin state", nil)
		return
	}
	if count > 0 {
		writeError(w, http.StatusForbidden, "setup_complete", "an admin account already exists", nil)
		return
	}

	var req authRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "request body must be valid JSON", nil)
		return
	}

	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	if req.Email == "" || !strings.Contains(req.Email, "@") {
		writeError(w, http.StatusBadRequest, "invalid_email", "a valid email is required", nil)
		return
	}
	if len(req.Password) < minPasswordLen {
		writeError(w, http.StatusBadRequest, "weak_password", "password must be at least 8 characters", nil)
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to process password", nil)
		return
	}

	a, err := createAdmin(r.Context(), s.db, req.Email, hash)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to create admin account", nil)
		return
	}

	token, err := auth.IssueToken(s.cfg.JWTSecret, a.ID, auth.SubjectAdmin)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to issue session", nil)
		return
	}

	writeJSON(w, http.StatusCreated, adminAuthResponse{Token: token, Record: a})
}

func (s *Server) handleAdminLogin(w http.ResponseWriter, r *http.Request) {
	var req authRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "request body must be valid JSON", nil)
		return
	}
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))

	a, err := getAdminByEmail(r.Context(), s.db, req.Email)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "invalid email or password", nil)
		return
	}

	ok, err := auth.VerifyPassword(req.Password, a.PasswordHash)
	if err != nil || !ok {
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "invalid email or password", nil)
		return
	}

	token, err := auth.IssueToken(s.cfg.JWTSecret, a.ID, auth.SubjectAdmin)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to issue session", nil)
		return
	}

	writeJSON(w, http.StatusOK, adminAuthResponse{Token: token, Record: a})
}
