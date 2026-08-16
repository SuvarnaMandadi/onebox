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

// handleSetupStatus is public and unauthenticated: the dashboard's auth
// pages use it to decide what to show before anyone is signed in — the
// dedicated superuser setup page (no admin yet), the plain login/signup
// forms (an admin exists), and whether signup should even be offered
// (registration_enabled, GET /api/settings itself being admin-only is
// exactly why that flag rides along here instead).
func (s *Server) handleSetupStatus(w http.ResponseWriter, r *http.Request) {
	count, err := countAdmins(r.Context(), s.db)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to check setup state", nil)
		return
	}
	regEnabled, err := registrationEnabled(r.Context(), s.db, s.cfg.JWTSecret)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to check registration settings", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"admin_exists": count > 0, "registration_enabled": regEnabled})
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

// handleAdminLogin is rate-limited by remote IP via the rateLimitByIP
// middleware — applied here since a superuser account is the highest-value
// brute-force target in the whole system. See handleSignup's doc comment
// in auth_handlers.go for why IP (not user identity) is the key pre-auth.
func (s *Server) handleAdminLogin(w http.ResponseWriter, r *http.Request) {
	var req authRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "request body must be valid JSON", nil)
		return
	}
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))

	a, err := getAdminByEmail(r.Context(), s.db, req.Email)
	if errors.Is(err, sql.ErrNoRows) {
		// Same generic code as a wrong password below — see
		// invalidLoginCode's doc comment (auth_handlers.go) for why this is
		// no longer a distinct "no_account".
		writeError(w, http.StatusUnauthorized, invalidLoginCode, invalidLoginMessage, nil)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to look up account", nil)
		return
	}

	ok, err := auth.VerifyPassword(req.Password, a.PasswordHash)
	if err != nil || !ok {
		writeError(w, http.StatusUnauthorized, invalidLoginCode, invalidLoginMessage, nil)
		return
	}

	token, err := auth.IssueToken(s.cfg.JWTSecret, a.ID, auth.SubjectAdmin)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to issue session", nil)
		return
	}

	writeJSON(w, http.StatusOK, adminAuthResponse{Token: token, Record: a})
}
