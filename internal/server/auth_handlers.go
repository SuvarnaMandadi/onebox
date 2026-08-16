package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"onebox/internal/auth"
)

type authRequest struct {
	Email     string `json:"email"`
	Password  string `json:"password"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

type authResponse struct {
	Token  string `json:"token"`
	Record *user  `json:"record"`
	// RecoveryPhrase is set only in the signup response — it's shown once
	// so the user can save it, then only its hash is ever stored. See
	// docs/api-reference.md for the recovery flow this backs.
	RecoveryPhrase string `json:"recovery_phrase,omitempty"`
}

const minPasswordLen = 8

// invalidLoginCode/invalidLoginMessage are the single generic error every
// login path (handleLogin here, handleAdminLogin in admin_handlers.go,
// handleUnifiedLogin in unified_login.go) returns for EITHER "no such
// account" or "wrong password" — deliberately indistinguishable, so a
// caller can't enumerate which registered emails exist by watching which
// error code comes back (account enumeration). Previously these two cases
// returned different codes ("no_account" vs "invalid_credentials"); that
// was cheap to abuse even before Fix 3's rate limiting existed, and still
// leaks real information with it in place.
const (
	invalidLoginCode    = "invalid_credentials"
	invalidLoginMessage = "Invalid email or password."
)

// handleSignup is rate-limited by remote IP via the rateLimitByIP
// middleware (server.go's route wiring) — a dedicated authRateLimiter
// budget, separate from rateLimiter's per-user chat/LLM throttling, since
// there's no account identity yet to key on at this point.
func (s *Server) handleSignup(w http.ResponseWriter, r *http.Request) {
	enabled, err := registrationEnabled(r.Context(), s.db, s.cfg.JWTSecret)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to check registration settings", nil)
		return
	}
	if !enabled {
		writeError(w, http.StatusForbidden, "registration_disabled", "New user registration is currently disabled.", nil)
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

	u, err := createUser(r.Context(), s.db, req.Email, hash, req.FirstName, req.LastName, phraseHash)
	if err != nil {
		if err == errEmailTaken {
			writeError(w, http.StatusConflict, "email_taken", "An account with this email already exists — log in instead?", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to create account", nil)
		return
	}

	token, err := auth.IssueToken(s.cfg.JWTSecret, u.ID, auth.SubjectUser)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to issue session", nil)
		return
	}

	writeJSON(w, http.StatusCreated, authResponse{Token: token, Record: u, RecoveryPhrase: phrase})
}

// handleLogin is rate-limited by remote IP via the rateLimitByIP middleware
// — see handleSignup's doc comment.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req authRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "request body must be valid JSON", nil)
		return
	}
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))

	u, err := getUserByEmail(r.Context(), s.db, req.Email)
	if errors.Is(err, sql.ErrNoRows) {
		// Deliberately the exact same code/message as a wrong password
		// below — see invalidLoginCredentials' doc comment for why: this
		// used to return a distinct "no_account" here, which let a caller
		// enumerate valid accounts for free (cheaply, before Fix 3's rate
		// limiting existed above; still a real information leak even with
		// it).
		writeError(w, http.StatusUnauthorized, invalidLoginCode, invalidLoginMessage, nil)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to look up account", nil)
		return
	}

	ok, err := auth.VerifyPassword(req.Password, u.PasswordHash)
	if err != nil || !ok {
		writeError(w, http.StatusUnauthorized, invalidLoginCode, invalidLoginMessage, nil)
		return
	}

	token, err := auth.IssueToken(s.cfg.JWTSecret, u.ID, auth.SubjectUser)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to issue session", nil)
		return
	}

	writeJSON(w, http.StatusOK, authResponse{Token: token, Record: u})
}

// generateAndHashRecoveryPhrase generates a fresh recovery phrase and
// returns both the plaintext (to show the caller once) and its hash (the
// only copy ever persisted).
func generateAndHashRecoveryPhrase() (phrase, hash string, err error) {
	phrase, err = auth.GenerateRecoveryPhrase()
	if err != nil {
		return "", "", err
	}
	hash, err = auth.HashPassword(auth.NormalizeRecoveryPhrase(phrase))
	if err != nil {
		return "", "", err
	}
	return phrase, hash, nil
}
