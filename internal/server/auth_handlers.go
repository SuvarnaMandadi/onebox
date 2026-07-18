package server

import (
	"encoding/json"
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
}

const minPasswordLen = 8

func (s *Server) handleSignup(w http.ResponseWriter, r *http.Request) {
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

	u, err := createUser(r.Context(), s.db, req.Email, hash, req.FirstName, req.LastName)
	if err != nil {
		if err == errEmailTaken {
			writeError(w, http.StatusConflict, "email_taken", "an account with that email already exists", nil)
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

	writeJSON(w, http.StatusCreated, authResponse{Token: token, Record: u})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req authRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "request body must be valid JSON", nil)
		return
	}
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))

	u, err := getUserByEmail(r.Context(), s.db, req.Email)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "invalid email or password", nil)
		return
	}

	ok, err := auth.VerifyPassword(req.Password, u.PasswordHash)
	if err != nil || !ok {
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "invalid email or password", nil)
		return
	}

	token, err := auth.IssueToken(s.cfg.JWTSecret, u.ID, auth.SubjectUser)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to issue session", nil)
		return
	}

	writeJSON(w, http.StatusOK, authResponse{Token: token, Record: u})
}
