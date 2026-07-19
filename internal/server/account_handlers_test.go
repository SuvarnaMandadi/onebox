package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMeAndUpdateProfile(t *testing.T) {
	srv := newTestServerWithFiles(t)
	_, token := signupUser(t, srv, "profile@example.com")

	rec := doAuth(t, srv, http.MethodGet, "/api/auth/me", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get me: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var me user
	if err := json.Unmarshal(rec.Body.Bytes(), &me); err != nil {
		t.Fatalf("decode me: %v", err)
	}
	if me.Email != "profile@example.com" {
		t.Fatalf("email = %q, want profile@example.com", me.Email)
	}

	t.Run("update first/last/phone", func(t *testing.T) {
		rec := doAuth(t, srv, http.MethodPatch, "/api/auth/me", token, updateProfileRequest{
			Email: "profile@example.com", FirstName: "Ada", LastName: "Lovelace", Phone: "555-0100",
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
		var updated user
		json.Unmarshal(rec.Body.Bytes(), &updated)
		if updated.FirstName != "Ada" || updated.LastName != "Lovelace" || updated.Phone != "555-0100" {
			t.Fatalf("got %+v", updated)
		}
	})

	t.Run("email change checks uniqueness case-insensitively", func(t *testing.T) {
		signupUser(t, srv, "taken@example.com")
		rec := doAuth(t, srv, http.MethodPatch, "/api/auth/me", token, updateProfileRequest{
			Email: "TAKEN@example.com", FirstName: "Ada", LastName: "Lovelace",
		})
		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409, body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("unauthenticated rejected", func(t *testing.T) {
		rec := doAuth(t, srv, http.MethodGet, "/api/auth/me", "", nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
	})
}

func TestChangePassword(t *testing.T) {
	srv := newTestServerWithFiles(t)
	_, token := signupUser(t, srv, "pwchange@example.com")

	t.Run("wrong current password rejected", func(t *testing.T) {
		rec := doAuth(t, srv, http.MethodPost, "/api/auth/change-password", token, changePasswordRequest{
			CurrentPassword: "wrong-password", NewPassword: "new-password-123",
		})
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401, body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("correct current password succeeds and new password logs in", func(t *testing.T) {
		rec := doAuth(t, srv, http.MethodPost, "/api/auth/change-password", token, changePasswordRequest{
			CurrentPassword: "hunter22222", NewPassword: "new-password-123",
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}

		loginRec := doJSON(t, srv, http.MethodPost, "/api/auth/login", authRequest{Email: "pwchange@example.com", Password: "new-password-123"})
		if loginRec.Code != http.StatusOK {
			t.Fatalf("login with new password: status = %d, body = %s", loginRec.Code, loginRec.Body.String())
		}
	})
}

func TestAdminAssistedPasswordReset(t *testing.T) {
	srv := newTestServerWithFiles(t)
	_, _ = signupUser(t, srv, "forgetful@example.com")
	adminToken := bootstrapAdmin(t, srv)

	t.Run("non-admin cannot mint a reset token", func(t *testing.T) {
		_, userToken := signupUser(t, srv, "other@example.com")
		rec := doAuth(t, srv, http.MethodPost, "/api/admins/password-resets", userToken, createPasswordResetRequest{Email: "forgetful@example.com"})
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401, body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("unknown email rejected", func(t *testing.T) {
		rec := doAuth(t, srv, http.MethodPost, "/api/admins/password-resets", adminToken, createPasswordResetRequest{Email: "nobody@example.com"})
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404, body = %s", rec.Code, rec.Body.String())
		}
	})

	rec := doAuth(t, srv, http.MethodPost, "/api/admins/password-resets", adminToken, createPasswordResetRequest{Email: "forgetful@example.com"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create reset token: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var created struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.Token == "" {
		t.Fatalf("expected a non-empty token")
	}

	t.Run("bad token rejected", func(t *testing.T) {
		rec := doJSON(t, srv, http.MethodPost, "/api/auth/reset-password", resetPasswordRequest{Token: "not-a-real-token", NewPassword: "brand-new-password"})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("valid token resets password and can't be reused", func(t *testing.T) {
		rec := doJSON(t, srv, http.MethodPost, "/api/auth/reset-password", resetPasswordRequest{Token: created.Token, NewPassword: "brand-new-password"})
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}

		loginRec := doJSON(t, srv, http.MethodPost, "/api/auth/login", authRequest{Email: "forgetful@example.com", Password: "brand-new-password"})
		if loginRec.Code != http.StatusOK {
			t.Fatalf("login with reset password: status = %d, body = %s", loginRec.Code, loginRec.Body.String())
		}

		reuseRec := doJSON(t, srv, http.MethodPost, "/api/auth/reset-password", resetPasswordRequest{Token: created.Token, NewPassword: "another-password-1"})
		if reuseRec.Code != http.StatusBadRequest {
			t.Fatalf("reuse status = %d, want 400, body = %s", reuseRec.Code, reuseRec.Body.String())
		}
	})
}

func TestUploadAvatar(t *testing.T) {
	srv := newTestServerWithFiles(t)
	_, token := signupUser(t, srv, "avatar@example.com")

	req := multipartUploadRequest(t, "/api/auth/me/avatar", "file", "face.png",
		[]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x02\x00\x00\x00\x90wS\xde"))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var updated user
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if updated.AvatarFileID == "" {
		t.Fatalf("expected avatar_file_id to be set")
	}

	t.Run("non-image rejected", func(t *testing.T) {
		req := multipartUploadRequest(t, "/api/auth/me/avatar", "file", "notes.txt", []byte("just text"))
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		srv.Router().ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
		}
	})
}

func TestSignupIncludesRecoveryPhraseOnce(t *testing.T) {
	srv := newTestServerWithFiles(t)

	rec := doJSON(t, srv, http.MethodPost, "/api/auth/signup", authRequest{Email: "recovery@example.com", Password: "hunter22222"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("signup: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp authResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	words := len(splitFields(resp.RecoveryPhrase))
	if words != 12 {
		t.Fatalf("recovery phrase has %d words, want 12: %q", words, resp.RecoveryPhrase)
	}

	// GET /api/auth/me must never expose it again — only its hash is kept.
	meRec := doAuth(t, srv, http.MethodGet, "/api/auth/me", resp.Token, nil)
	if strings.Contains(meRec.Body.String(), "recovery_phrase") {
		t.Fatalf("GET /api/auth/me leaked recovery phrase field: %s", meRec.Body.String())
	}
}

func TestAdminSignupIncludesRecoveryPhraseOnce(t *testing.T) {
	srv := newTestServerWithFiles(t)

	rec := doJSON(t, srv, http.MethodPost, "/api/admins/signup", authRequest{Email: "admin@example.com", Password: "hunter22222"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("signup: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp adminAuthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(splitFields(resp.RecoveryPhrase)) != 12 {
		t.Fatalf("recovery phrase has %d words, want 12: %q", len(splitFields(resp.RecoveryPhrase)), resp.RecoveryPhrase)
	}
}

func TestRecoverPasswordForUser(t *testing.T) {
	srv := newTestServerWithFiles(t)

	signupRec := doJSON(t, srv, http.MethodPost, "/api/auth/signup", authRequest{Email: "forgetful@example.com", Password: "hunter22222"})
	var signupResp authResponse
	json.Unmarshal(signupRec.Body.Bytes(), &signupResp)

	t.Run("wrong phrase rejected", func(t *testing.T) {
		rec := doJSON(t, srv, http.MethodPost, "/api/auth/recover-password", recoverPasswordRequest{
			Email: "forgetful@example.com", RecoveryPhrase: "wrong words entirely not the real phrase at all whatsoever nope",
			NewPassword: "brand-new-password-1",
		})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("correct phrase resets password", func(t *testing.T) {
		rec := doJSON(t, srv, http.MethodPost, "/api/auth/recover-password", recoverPasswordRequest{
			Email: "forgetful@example.com", RecoveryPhrase: signupResp.RecoveryPhrase,
			NewPassword: "brand-new-password-1",
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}

		loginRec := doJSON(t, srv, http.MethodPost, "/api/auth/login", authRequest{Email: "forgetful@example.com", Password: "brand-new-password-1"})
		if loginRec.Code != http.StatusOK {
			t.Fatalf("login with new password: status = %d, body = %s", loginRec.Code, loginRec.Body.String())
		}
	})

	t.Run("case and whitespace insensitive", func(t *testing.T) {
		messyPhrase := "  " + strings.ToUpper(signupResp.RecoveryPhrase) + "  "
		rec := doJSON(t, srv, http.MethodPost, "/api/auth/recover-password", recoverPasswordRequest{
			Email: "forgetful@example.com", RecoveryPhrase: messyPhrase,
			NewPassword: "another-new-password-2",
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
	})
}

func TestRecoverPasswordForAdmin(t *testing.T) {
	srv := newTestServerWithFiles(t)

	signupRec := doJSON(t, srv, http.MethodPost, "/api/admins/signup", authRequest{Email: "admin@example.com", Password: "hunter22222"})
	var signupResp adminAuthResponse
	json.Unmarshal(signupRec.Body.Bytes(), &signupResp)

	rec := doJSON(t, srv, http.MethodPost, "/api/auth/recover-password", recoverPasswordRequest{
		Email: "admin@example.com", RecoveryPhrase: signupResp.RecoveryPhrase,
		NewPassword: "brand-new-admin-password-1", Role: "admin",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	loginRec := doJSON(t, srv, http.MethodPost, "/api/admins/login", authRequest{Email: "admin@example.com", Password: "brand-new-admin-password-1"})
	if loginRec.Code != http.StatusOK {
		t.Fatalf("admin login with new password: status = %d, body = %s", loginRec.Code, loginRec.Body.String())
	}

	// A "role":"admin" recovery request must not touch a _users account
	// that happens to share the email.
	t.Run("wrong role does not cross accounts", func(t *testing.T) {
		srv2 := newTestServerWithFiles(t)
		userSignupRec := doJSON(t, srv2, http.MethodPost, "/api/auth/signup", authRequest{Email: "shared@example.com", Password: "hunter22222"})
		var userResp authResponse
		json.Unmarshal(userSignupRec.Body.Bytes(), &userResp)

		rec := doJSON(t, srv2, http.MethodPost, "/api/auth/recover-password", recoverPasswordRequest{
			Email: "shared@example.com", RecoveryPhrase: userResp.RecoveryPhrase,
			NewPassword: "irrelevant-password-1", Role: "admin",
		})
		if rec.Code == http.StatusOK {
			t.Fatalf("a user's recovery phrase must not work against role=admin (no admin exists at all here)")
		}
	})
}

func TestRegenerateRecoveryPhrase(t *testing.T) {
	srv := newTestServerWithFiles(t)
	signupRec := doJSON(t, srv, http.MethodPost, "/api/auth/signup", authRequest{Email: "regen@example.com", Password: "hunter22222"})
	var signupResp authResponse
	json.Unmarshal(signupRec.Body.Bytes(), &signupResp)

	t.Run("wrong current password rejected", func(t *testing.T) {
		rec := doAuth(t, srv, http.MethodPost, "/api/auth/regenerate-recovery-phrase", signupResp.Token, regenerateRecoveryPhraseRequest{CurrentPassword: "wrong"})
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401, body = %s", rec.Code, rec.Body.String())
		}
	})

	rec := doAuth(t, srv, http.MethodPost, "/api/auth/regenerate-recovery-phrase", signupResp.Token, regenerateRecoveryPhraseRequest{CurrentPassword: "hunter22222"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var regenResp struct {
		RecoveryPhrase string `json:"recovery_phrase"`
	}
	json.Unmarshal(rec.Body.Bytes(), &regenResp)
	if regenResp.RecoveryPhrase == "" || regenResp.RecoveryPhrase == signupResp.RecoveryPhrase {
		t.Fatalf("expected a fresh, different recovery phrase, got %q (original was %q)", regenResp.RecoveryPhrase, signupResp.RecoveryPhrase)
	}

	t.Run("old phrase no longer works", func(t *testing.T) {
		rec := doJSON(t, srv, http.MethodPost, "/api/auth/recover-password", recoverPasswordRequest{
			Email: "regen@example.com", RecoveryPhrase: signupResp.RecoveryPhrase, NewPassword: "whatever-password-1",
		})
		if rec.Code == http.StatusOK {
			t.Fatalf("the old recovery phrase must be invalidated after regenerating")
		}
	})
	t.Run("new phrase works", func(t *testing.T) {
		rec := doJSON(t, srv, http.MethodPost, "/api/auth/recover-password", recoverPasswordRequest{
			Email: "regen@example.com", RecoveryPhrase: regenResp.RecoveryPhrase, NewPassword: "whatever-password-2",
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
	})
}

func TestRegenerateRecoveryPhraseForAdmin(t *testing.T) {
	srv := newTestServerWithFiles(t)
	signupRec := doJSON(t, srv, http.MethodPost, "/api/admins/signup", authRequest{Email: "admin@example.com", Password: "hunter22222"})
	var signupResp adminAuthResponse
	json.Unmarshal(signupRec.Body.Bytes(), &signupResp)

	rec := doAuth(t, srv, http.MethodPost, "/api/auth/regenerate-recovery-phrase", signupResp.Token, regenerateRecoveryPhraseRequest{CurrentPassword: "hunter22222"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var regenResp struct {
		RecoveryPhrase string `json:"recovery_phrase"`
	}
	json.Unmarshal(rec.Body.Bytes(), &regenResp)
	if regenResp.RecoveryPhrase == "" || regenResp.RecoveryPhrase == signupResp.RecoveryPhrase {
		t.Fatalf("expected a fresh, different recovery phrase")
	}
}

func splitFields(s string) []string {
	return strings.Fields(s)
}

// TestAvatarHiddenFromFileListing covers the hand-tested feedback that
// profile photos must never show up in File Storage: an avatar upload
// must not appear in GET /api/files, and removing it (not deleting via
// the Files list, which can't even see it) must revert to initials.
func TestAvatarHiddenFromFileListing(t *testing.T) {
	srv := newTestServerWithFiles(t)
	_, token := signupUser(t, srv, "avatarhidden@example.com")

	// A regular file upload should still show up.
	req := multipartUploadRequest(t, "/api/files", "file", "regular.txt", []byte("hello"))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload regular file: status = %d, body = %s", rec.Code, rec.Body.String())
	}

	avatarReq := multipartUploadRequest(t, "/api/auth/me/avatar", "file", "face.png",
		[]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x02\x00\x00\x00\x90wS\xde"))
	avatarReq.Header.Set("Authorization", "Bearer "+token)
	avatarRec := httptest.NewRecorder()
	srv.Router().ServeHTTP(avatarRec, avatarReq)
	if avatarRec.Code != http.StatusOK {
		t.Fatalf("upload avatar: status = %d, body = %s", avatarRec.Code, avatarRec.Body.String())
	}

	listRec := doAuth(t, srv, http.MethodGet, "/api/files", token, nil)
	var listResp struct {
		Items []fileRecord `json:"items"`
		Total int          `json:"total"`
	}
	json.Unmarshal(listRec.Body.Bytes(), &listResp)
	if listResp.Total != 1 || len(listResp.Items) != 1 {
		t.Fatalf("expected only the regular file in the listing, got total=%d items=%d", listResp.Total, len(listResp.Items))
	}

	removeRec := doAuth(t, srv, http.MethodDelete, "/api/auth/me/avatar", token, nil)
	if removeRec.Code != http.StatusOK {
		t.Fatalf("remove avatar: status = %d, body = %s", removeRec.Code, removeRec.Body.String())
	}
	var removed user
	json.Unmarshal(removeRec.Body.Bytes(), &removed)
	if removed.AvatarFileID != "" {
		t.Fatalf("expected avatar_file_id to be cleared, got %q", removed.AvatarFileID)
	}
}
