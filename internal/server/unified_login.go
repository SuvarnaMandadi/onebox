package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"onebox/internal/auth"
)

// unifiedLoginResponse is deliberately thin — just enough for the
// dashboard to store a token and know which "me" endpoint to call next
// (see loadAccount in app.js) — rather than embedding the full admin/user
// record and forcing every caller to branch on shape.
type unifiedLoginResponse struct {
	Token string `json:"token"`
	Role  string `json:"role"` // "admin" or "user"
}

// handleUnifiedLogin is the one login the dashboard's single login page
// calls: the caller submits just email + password, with no admin/user
// choice, and the backend decides which account type matched. This is
// the "backend determines the role after authentication" requirement —
// distinct from the still-separate POST /api/admins/login and
// POST /api/auth/login, which remain as direct, single-purpose API
// endpoints for scripts/SDKs that already know which kind of account
// they're authenticating (and, notably, so a third-party app's own user
// login screen never accidentally doubles as an admin login path).
//
// Admins are checked first: an email can independently exist in both
// _admins and _users (the two tables have no cross-uniqueness
// constraint), and a superuser should never be shadowed by a
// same-address regular-user account.
// handleUnifiedLogin is rate-limited by remote IP via the rateLimitByIP
// middleware. This is the dashboard's actual login page, so it's exactly
// as much of a brute-force surface as handleLogin/handleAdminLogin (see
// their doc comments) despite trying both account tables.
func (s *Server) handleUnifiedLogin(w http.ResponseWriter, r *http.Request) {
	var req authRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "request body must be valid JSON", nil)
		return
	}
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))

	a, err := getAdminByEmail(r.Context(), s.db, req.Email)
	if err == nil {
		ok, verr := auth.VerifyPassword(req.Password, a.PasswordHash)
		if verr != nil || !ok {
			writeError(w, http.StatusUnauthorized, invalidLoginCode, invalidLoginMessage, nil)
			return
		}
		token, terr := auth.IssueToken(s.cfg.JWTSecret, a.ID, auth.SubjectAdmin)
		if terr != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to issue session", nil)
			return
		}
		writeJSON(w, http.StatusOK, unifiedLoginResponse{Token: token, Role: "admin"})
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to look up account", nil)
		return
	}

	u, err := getUserByEmail(r.Context(), s.db, req.Email)
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
	writeJSON(w, http.StatusOK, unifiedLoginResponse{Token: token, Role: "user"})
}
