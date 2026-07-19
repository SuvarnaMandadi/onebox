package server

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"onebox/internal/auth"
)

// handleListAdmins is admin-only: powers the Settings page's admin
// management panel, where an existing admin can see who else has admin
// access before deciding whether to demote them.
func (s *Server) handleListAdmins(w http.ResponseWriter, r *http.Request) {
	admins, err := listAdmins(r.Context(), s.db)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to list admins", nil)
		return
	}
	if admins == nil {
		admins = []*admin{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": admins})
}

type promoteRequest struct {
	Email string `json:"email"`
}

// handlePromoteToAdmin is admin-only. It creates a *new*, separate
// _admins account for the given email (copying the display name over if
// a matching _users account exists) with a throwaway random password the
// caller never sees directly — instead this mints a one-time reset token
// for it, the same admin-assisted-reset mechanism already used for
// _users, so the promoted person sets their own admin password by
// redeeming it. Their original _users account (and everything it owns)
// is untouched; they end up with two separate logins, which is the
// simplest safe option given _admins and _users are independent identity
// tables. See docs/api-reference.md for the full promote/demote flow.
func (s *Server) handlePromoteToAdmin(w http.ResponseWriter, r *http.Request) {
	var req promoteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "request body must be valid JSON", nil)
		return
	}
	email := strings.TrimSpace(strings.ToLower(req.Email))
	if email == "" || !strings.Contains(email, "@") {
		writeError(w, http.StatusBadRequest, "invalid_email", "a valid email is required", nil)
		return
	}

	if _, err := getAdminByEmail(r.Context(), s.db, email); err == nil {
		writeError(w, http.StatusConflict, "already_admin", "this email is already an admin", nil)
		return
	} else if err != sql.ErrNoRows {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to check existing admins", nil)
		return
	}

	firstName, lastName := "", ""
	if u, err := getUserByEmail(r.Context(), s.db, email); err == nil {
		firstName, lastName = u.FirstName, u.LastName
	}

	throwawayPassword := make([]byte, 24)
	if _, err := rand.Read(throwawayPassword); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to generate credentials", nil)
		return
	}
	hash, err := auth.HashPassword(hex.EncodeToString(throwawayPassword))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to process credentials", nil)
		return
	}
	phrase, phraseHash, err := generateAndHashRecoveryPhrase()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to generate recovery phrase", nil)
		return
	}

	a, err := createAdmin(r.Context(), s.db, email, hash, firstName, lastName, phraseHash)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to create admin account", nil)
		return
	}

	token, expiresAt, err := createPasswordResetToken(r.Context(), s.db, "admin", a.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to create reset token", nil)
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"email":           a.Email,
		"reset_token":     token,
		"expires_at":      expiresAt,
		"recovery_phrase": phrase,
	})
}

type demoteRequest struct {
	Email string `json:"email"`
}

// handleDemoteAdmin is admin-only and refuses to remove the last
// remaining admin account, since that would lock everyone out of admin
// features (and of promoting anyone back in).
func (s *Server) handleDemoteAdmin(w http.ResponseWriter, r *http.Request) {
	var req demoteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "request body must be valid JSON", nil)
		return
	}
	email := strings.TrimSpace(strings.ToLower(req.Email))

	count, err := countAdmins(r.Context(), s.db)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to check admin count", nil)
		return
	}
	if count <= 1 {
		writeError(w, http.StatusBadRequest, "last_admin", "can't remove the last remaining admin account", nil)
		return
	}

	a, err := getAdminByEmail(r.Context(), s.db, email)
	if err == sql.ErrNoRows {
		writeError(w, http.StatusNotFound, "not_found", "no admin account with that email", nil)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to look up admin", nil)
		return
	}

	if err := deleteAdmin(r.Context(), s.db, a.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to remove admin", nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleAdminMe / handleUpdateAdminMe / handleUploadAdminAvatar give admin
// accounts the same Account-page profile experience as regular users,
// now that _admins carries the same profile columns.
func (s *Server) handleAdminMe(w http.ResponseWriter, r *http.Request) {
	aid, ok := authAdminID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid_token", "session token is invalid or expired", nil)
		return
	}
	a, err := getAdminByID(r.Context(), s.db, aid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to load profile", nil)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

type updateAdminProfileRequest struct {
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Phone     string `json:"phone"`
}

func (s *Server) handleUpdateAdminMe(w http.ResponseWriter, r *http.Request) {
	aid, ok := authAdminID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid_token", "session token is invalid or expired", nil)
		return
	}
	var req updateAdminProfileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "request body must be valid JSON", nil)
		return
	}
	a, err := updateAdminProfile(r.Context(), s.db, aid, strings.TrimSpace(req.FirstName), strings.TrimSpace(req.LastName), strings.TrimSpace(req.Phone))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to update profile", nil)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (s *Server) handleUploadAdminAvatar(w http.ResponseWriter, r *http.Request) {
	aid, ok := authAdminID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid_token", "session token is invalid or expired", nil)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxUploadSize)
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_upload", "file too large or not a valid multipart/form-data upload", nil)
		return
	}
	defer r.MultipartForm.RemoveAll()

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing_file", `expected a "file" multipart field`, nil)
		return
	}
	defer file.Close()

	content, err := io.ReadAll(file)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_upload", "failed to read uploaded file", nil)
		return
	}
	mime := http.DetectContentType(content)
	if !strings.HasPrefix(mime, "image/") {
		writeError(w, http.StatusBadRequest, "invalid_file_type", "avatar must be an image file", map[string]any{"received": mime})
		return
	}

	fileID, err := storeFileContent(s.cfg.FilesDir, content)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to store avatar", nil)
		return
	}
	if _, err := createFileRecord(r.Context(), s.db, fileID, aid, header.Filename, mime, int64(len(content))); err != nil {
		removeStoredFile(s.cfg.FilesDir, fileID)
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to record avatar file", nil)
		return
	}

	a, err := updateAdminAvatar(r.Context(), s.db, aid, fileID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to update avatar", nil)
		return
	}
	writeJSON(w, http.StatusOK, a)
}
