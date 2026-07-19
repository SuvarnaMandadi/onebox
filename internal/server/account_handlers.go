package server

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"onebox/internal/auth"
)

// handleMe returns the signed-in _users account's own profile. Used by the
// dashboard's Account page and the sidebar's avatar/name display.
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	uid, ok := authUserID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid_token", "session token is invalid or expired", nil)
		return
	}
	u, err := getUserByID(r.Context(), s.db, uid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to load profile", nil)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

type updateProfileRequest struct {
	Email     string `json:"email"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Phone     string `json:"phone"`
}

// handleUpdateMe updates the caller's own editable profile fields. A
// changed email is re-validated and re-checked for uniqueness
// case-insensitively, the same rule signup/login already apply.
func (s *Server) handleUpdateMe(w http.ResponseWriter, r *http.Request) {
	uid, ok := authUserID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid_token", "session token is invalid or expired", nil)
		return
	}

	var req updateProfileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "request body must be valid JSON", nil)
		return
	}

	email := strings.TrimSpace(strings.ToLower(req.Email))
	if email == "" || !strings.Contains(email, "@") {
		writeError(w, http.StatusBadRequest, "invalid_email", "a valid email is required", nil)
		return
	}

	u, err := updateUserProfile(r.Context(), s.db, uid, email, strings.TrimSpace(req.FirstName), strings.TrimSpace(req.LastName), strings.TrimSpace(req.Phone))
	if err != nil {
		if err == errEmailTaken {
			writeError(w, http.StatusConflict, "email_taken", "an account with that email already exists", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to update profile", nil)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

// handleUploadAvatar accepts a multipart "file" field, stores it through the
// same file storage as any other upload, and points the caller's
// avatar_file_id at it. The dashboard falls back to initials when a user
// has no avatar_file_id set.
func (s *Server) handleUploadAvatar(w http.ResponseWriter, r *http.Request) {
	uid, ok := authUserID(r.Context())
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
	if _, err := createFileRecord(r.Context(), s.db, fileID, uid, header.Filename, mime, int64(len(content))); err != nil {
		removeStoredFile(s.cfg.FilesDir, fileID)
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to record avatar file", nil)
		return
	}

	u, err := updateUserAvatar(r.Context(), s.db, uid, fileID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to update avatar", nil)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	uid, ok := authUserID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid_token", "session token is invalid or expired", nil)
		return
	}

	var req changePasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "request body must be valid JSON", nil)
		return
	}
	if len(req.NewPassword) < minPasswordLen {
		writeError(w, http.StatusBadRequest, "weak_password", "new password must be at least 8 characters", nil)
		return
	}

	u, err := getUserByID(r.Context(), s.db, uid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to load account", nil)
		return
	}
	ok2, err := auth.VerifyPassword(req.CurrentPassword, u.PasswordHash)
	if err != nil || !ok2 {
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "current password is incorrect", nil)
		return
	}

	hash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to process password", nil)
		return
	}
	if err := updateUserPasswordHash(r.Context(), s.db, uid, hash); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to update password", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

const passwordResetTTL = 1 * time.Hour

type createPasswordResetRequest struct {
	Email string `json:"email"`
}

// handleCreatePasswordReset is admin-only: since v0.2 has no SMTP
// integration, self-service "forgot password" email isn't possible yet.
// Instead an admin looks up the account by email from the dashboard and
// mints a one-time token here, which they then hand to the user out of
// band (chat, a support ticket, in person). The user pastes the token into
// the dashboard's reset-password page, which calls
// POST /api/auth/reset-password to consume it.
//
// The clean seam for future work: once SMTP settings exist, this same
// token/table can be emailed automatically from a new, unauthenticated
// "forgot password" endpoint that calls this same createPasswordResetToken
// helper — no schema or flow change needed, just a new caller and an email.
func (s *Server) handleCreatePasswordReset(w http.ResponseWriter, r *http.Request) {
	var req createPasswordResetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "request body must be valid JSON", nil)
		return
	}
	email := strings.TrimSpace(strings.ToLower(req.Email))

	u, err := getUserByEmail(r.Context(), s.db, email)
	if err == sql.ErrNoRows {
		writeError(w, http.StatusNotFound, "not_found", "no user account with that email", nil)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to look up user", nil)
		return
	}

	token, expiresAt, err := createPasswordResetToken(r.Context(), s.db, "user", u.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to create reset token", nil)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"token":      token,
		"expires_at": expiresAt,
		"user_id":    u.ID,
		"email":      u.Email,
	})
}

// createPasswordResetToken mints a one-time token for either a _users or
// an _admins row (subjectType distinguishes which table
// handleResetPassword should update when the token is redeemed). Used by
// the admin-assisted reset flow and by POST /api/admins/promote (which
// mints an admin-typed token for the newly created admin row so its
// throwaway initial password can be replaced immediately).
func createPasswordResetToken(ctx context.Context, sqlDB *sql.DB, subjectType, subjectID string) (token, expiresAt string, err error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("generate token: %w", err)
	}
	token = hex.EncodeToString(buf)
	expiresAt = time.Now().Add(passwordResetTTL).UTC().Format(time.RFC3339Nano)

	_, err = sqlDB.ExecContext(ctx,
		`INSERT INTO _password_resets (token, user_id, subject_type, expires_at) VALUES (?, ?, ?, ?)`,
		token, subjectID, subjectType, expiresAt,
	)
	if err != nil {
		return "", "", fmt.Errorf("insert reset token: %w", err)
	}
	return token, expiresAt, nil
}

type resetPasswordRequest struct {
	Token       string `json:"token"`
	NewPassword string `json:"new_password"`
}

// handleResetPassword is unauthenticated by design: a user who forgot their
// password has no session token to present. It's gated instead by
// possession of the one-time token an admin generated for them.
func (s *Server) handleResetPassword(w http.ResponseWriter, r *http.Request) {
	var req resetPasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "request body must be valid JSON", nil)
		return
	}
	if len(req.NewPassword) < minPasswordLen {
		writeError(w, http.StatusBadRequest, "weak_password", "password must be at least 8 characters", nil)
		return
	}
	if req.Token == "" {
		writeError(w, http.StatusBadRequest, "invalid_token", "a reset token is required", nil)
		return
	}

	var subjectID, subjectType, expiresAt string
	var used int
	err := s.db.QueryRowContext(r.Context(), `SELECT user_id, subject_type, expires_at, used FROM _password_resets WHERE token = ?`, req.Token).
		Scan(&subjectID, &subjectType, &expiresAt, &used)
	if err == sql.ErrNoRows {
		writeError(w, http.StatusBadRequest, "invalid_token", "reset token is invalid", nil)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to look up reset token", nil)
		return
	}
	if used != 0 {
		writeError(w, http.StatusBadRequest, "invalid_token", "reset token has already been used", nil)
		return
	}
	expires, err := time.Parse(time.RFC3339Nano, expiresAt)
	if err != nil || time.Now().After(expires) {
		writeError(w, http.StatusBadRequest, "invalid_token", "reset token has expired", nil)
		return
	}

	hash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to process password", nil)
		return
	}
	if err := setPasswordHashForSubject(r.Context(), s.db, subjectType, subjectID, hash); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to update password", nil)
		return
	}
	if _, err := s.db.ExecContext(r.Context(), `UPDATE _password_resets SET used = 1 WHERE token = ?`, req.Token); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to consume reset token", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// setPasswordHashForSubject dispatches to the right table's password
// update based on the subject_type recorded on a _password_resets row.
func setPasswordHashForSubject(ctx context.Context, sqlDB *sql.DB, subjectType, subjectID, hash string) error {
	if subjectType == "admin" {
		return updateAdminPasswordHash(ctx, sqlDB, subjectID, hash)
	}
	return updateUserPasswordHash(ctx, sqlDB, subjectID, hash)
}

type recoverPasswordRequest struct {
	Email          string `json:"email"`
	RecoveryPhrase string `json:"recovery_phrase"`
	NewPassword    string `json:"new_password"`
	Role           string `json:"role"` // "user" (default) or "admin"
}

// handleRecoverPassword is the primary self-service "forgot password"
// path: email + the 12-word recovery phrase shown once at signup, in
// place of a password the caller has forgotten. Unauthenticated by
// design, same as handleResetPassword — gated by possession of the
// phrase instead of a token.
func (s *Server) handleRecoverPassword(w http.ResponseWriter, r *http.Request) {
	var req recoverPasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "request body must be valid JSON", nil)
		return
	}
	if len(req.NewPassword) < minPasswordLen {
		writeError(w, http.StatusBadRequest, "weak_password", "password must be at least 8 characters", nil)
		return
	}
	email := strings.TrimSpace(strings.ToLower(req.Email))
	phrase := auth.NormalizeRecoveryPhrase(req.RecoveryPhrase)
	if email == "" || phrase == "" {
		writeError(w, http.StatusBadRequest, "invalid_body", "email and recovery_phrase are required", nil)
		return
	}

	var id, hash, recoveryHash string
	var updatePassword func(ctx context.Context, sqlDB *sql.DB, id, hash string) error

	if req.Role == "admin" {
		a, err := getAdminByEmail(r.Context(), s.db, email)
		if err == sql.ErrNoRows {
			writeError(w, http.StatusBadRequest, "invalid_recovery", "email or recovery phrase is incorrect", nil)
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to look up account", nil)
			return
		}
		id, recoveryHash = a.ID, a.RecoveryPhraseHash
		updatePassword = updateAdminPasswordHash
	} else {
		u, err := getUserByEmail(r.Context(), s.db, email)
		if err == sql.ErrNoRows {
			writeError(w, http.StatusBadRequest, "invalid_recovery", "email or recovery phrase is incorrect", nil)
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to look up account", nil)
			return
		}
		id, recoveryHash = u.ID, u.RecoveryPhraseHash
		updatePassword = updateUserPasswordHash
	}

	if recoveryHash == "" {
		writeError(w, http.StatusBadRequest, "invalid_recovery", "this account has no recovery phrase set", nil)
		return
	}
	ok, err := auth.VerifyPassword(phrase, recoveryHash)
	if err != nil || !ok {
		writeError(w, http.StatusBadRequest, "invalid_recovery", "email or recovery phrase is incorrect", nil)
		return
	}

	hash, err = auth.HashPassword(req.NewPassword)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to process password", nil)
		return
	}
	if err := updatePassword(r.Context(), s.db, id, hash); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to update password", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

type regenerateRecoveryPhraseRequest struct {
	CurrentPassword string `json:"current_password"`
}

// handleRegenerateRecoveryPhrase mints a fresh recovery phrase for
// whichever identity (admin or user) the caller is authenticated as,
// requiring their current password first, and invalidating the previous
// phrase (there's only ever one hash stored, so generating a new one
// naturally retires the old one).
func (s *Server) handleRegenerateRecoveryPhrase(w http.ResponseWriter, r *http.Request) {
	var req regenerateRecoveryPhraseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "request body must be valid JSON", nil)
		return
	}

	if aid, ok := authAdminID(r.Context()); ok {
		a, err := getAdminByID(r.Context(), s.db, aid)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to load account", nil)
			return
		}
		ok2, err := auth.VerifyPassword(req.CurrentPassword, a.PasswordHash)
		if err != nil || !ok2 {
			writeError(w, http.StatusUnauthorized, "invalid_credentials", "current password is incorrect", nil)
			return
		}
		phrase, phraseHash, err := generateAndHashRecoveryPhrase()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to generate recovery phrase", nil)
			return
		}
		if err := updateAdminRecoveryPhraseHash(r.Context(), s.db, aid, phraseHash); err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to save recovery phrase", nil)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"recovery_phrase": phrase})
		return
	}

	uid, ok := authUserID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid_token", "session token is invalid or expired", nil)
		return
	}
	u, err := getUserByID(r.Context(), s.db, uid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to load account", nil)
		return
	}
	ok2, err := auth.VerifyPassword(req.CurrentPassword, u.PasswordHash)
	if err != nil || !ok2 {
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "current password is incorrect", nil)
		return
	}
	phrase, phraseHash, err := generateAndHashRecoveryPhrase()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to generate recovery phrase", nil)
		return
	}
	if err := updateUserRecoveryPhraseHash(r.Context(), s.db, uid, phraseHash); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to save recovery phrase", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"recovery_phrase": phrase})
}
