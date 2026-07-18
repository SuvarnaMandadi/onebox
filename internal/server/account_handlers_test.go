package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
