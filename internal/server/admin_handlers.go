package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"onebox/internal/auth"
)

type adminAuthResponse struct {
	Token  string `json:"token"`
	Record *admin `json:"record"`
	// RecoveryPhrase is set only in the signup response — see authResponse.
	RecoveryPhrase string `json:"recovery_phrase,omitempty"`
}

// handleSetupStatus is public and unauthenticated: the dashboard's signup
// page uses it to decide whether to offer plain "Sign up" (creating a
// regular _users account) or to bootstrap the very first admin — a fresh
// instance's first account is always the owner/admin, so there's no
// separate "admin signup" flow to choose once this is true.
func (s *Server) handleSetupStatus(w http.ResponseWriter, r *http.Request) {
	count, err := countAdmins(r.Context(), s.db)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to check setup state", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"admin_exists": count > 0})
}

// handleAdminSignup creates the first dashboard administrator. Once at
// least one admin exists, this endpoint is closed — further admins are
// created via POST /api/admins/promote by an existing admin.
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
	req.FirstName = strings.TrimSpace(req.FirstName)
	req.LastName = strings.TrimSpace(req.LastName)

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to process password", nil)
		return
	}

	phrase, phraseHash, err := generateAndHashRecoveryPhrase()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to generate recovery phrase", nil)
		return
	}

	a, err := createAdmin(r.Context(), s.db, req.Email, hash, req.FirstName, req.LastName, phraseHash)
	if err != nil {
		if err == errEmailTaken {
			writeError(w, http.StatusConflict, "email_taken", "An account with this email already exists — log in instead?", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to create admin account", nil)
		return
	}

	token, err := auth.IssueToken(s.cfg.JWTSecret, a.ID, auth.SubjectAdmin)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to issue session", nil)
		return
	}

	writeJSON(w, http.StatusCreated, adminAuthResponse{Token: token, Record: a, RecoveryPhrase: phrase})
}

func (s *Server) handleAdminLogin(w http.ResponseWriter, r *http.Request) {
	var req authRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "request body must be valid JSON", nil)
		return
	}
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))

	a, err := getAdminByEmail(r.Context(), s.db, req.Email)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusUnauthorized, "no_account", "No admin account found with this email.", nil)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to look up account", nil)
		return
	}

	ok, err := auth.VerifyPassword(req.Password, a.PasswordHash)
	if err != nil || !ok {
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "Invalid email or password.", nil)
		return
	}

	token, err := auth.IssueToken(s.cfg.JWTSecret, a.ID, auth.SubjectAdmin)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to issue session", nil)
		return
	}

	writeJSON(w, http.StatusOK, adminAuthResponse{Token: token, Record: a})
}
