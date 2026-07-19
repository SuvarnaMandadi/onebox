package server

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestPromoteAndDemoteAdmin(t *testing.T) {
	srv := newTestServerWithFiles(t)
	adminToken := bootstrapAdmin(t, srv)
	signupUser(t, srv, "future-admin@example.com")

	t.Run("non-admin cannot promote", func(t *testing.T) {
		_, userToken := signupUser(t, srv, "rando@example.com")
		rec := doAuth(t, srv, http.MethodPost, "/api/admins/promote", userToken, promoteRequest{Email: "future-admin@example.com"})
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401, body = %s", rec.Code, rec.Body.String())
		}
	})

	rec := doAuth(t, srv, http.MethodPost, "/api/admins/promote", adminToken, promoteRequest{Email: "future-admin@example.com"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("promote: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var promoted struct {
		Email          string `json:"email"`
		ResetToken     string `json:"reset_token"`
		RecoveryPhrase string `json:"recovery_phrase"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &promoted); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if promoted.ResetToken == "" || promoted.RecoveryPhrase == "" {
		t.Fatalf("expected both a reset token and a recovery phrase, got %+v", promoted)
	}

	t.Run("promoting the same email twice is rejected", func(t *testing.T) {
		rec := doAuth(t, srv, http.MethodPost, "/api/admins/promote", adminToken, promoteRequest{Email: "future-admin@example.com"})
		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409, body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("promoted account redeems its reset token and logs in as admin", func(t *testing.T) {
		resetRec := doJSON(t, srv, http.MethodPost, "/api/auth/reset-password", resetPasswordRequest{
			Token: promoted.ResetToken, NewPassword: "new-admin-password-1",
		})
		if resetRec.Code != http.StatusOK {
			t.Fatalf("reset: status = %d, body = %s", resetRec.Code, resetRec.Body.String())
		}
		loginRec := doJSON(t, srv, http.MethodPost, "/api/admins/login", authRequest{Email: "future-admin@example.com", Password: "new-admin-password-1"})
		if loginRec.Code != http.StatusOK {
			t.Fatalf("admin login: status = %d, body = %s", loginRec.Code, loginRec.Body.String())
		}
	})

	t.Run("original user account is untouched", func(t *testing.T) {
		loginRec := doJSON(t, srv, http.MethodPost, "/api/auth/login", authRequest{Email: "future-admin@example.com", Password: "hunter22222"})
		if loginRec.Code != http.StatusOK {
			t.Fatalf("original user login should still work: status = %d, body = %s", loginRec.Code, loginRec.Body.String())
		}
	})

	t.Run("list admins shows both", func(t *testing.T) {
		rec := doAuth(t, srv, http.MethodGet, "/api/admins", adminToken, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
		var resp struct {
			Items []admin `json:"items"`
		}
		json.Unmarshal(rec.Body.Bytes(), &resp)
		if len(resp.Items) != 2 {
			t.Fatalf("got %d admins, want 2", len(resp.Items))
		}
	})

	t.Run("cannot demote the last admin", func(t *testing.T) {
		srv2 := newTestServerWithFiles(t)
		soleAdminToken := bootstrapAdmin(t, srv2)
		rec := doAuth(t, srv2, http.MethodPost, "/api/admins/demote", soleAdminToken, demoteRequest{Email: "admin@example.com"})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("demote removes admin access", func(t *testing.T) {
		rec := doAuth(t, srv, http.MethodPost, "/api/admins/demote", adminToken, demoteRequest{Email: "future-admin@example.com"})
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204, body = %s", rec.Code, rec.Body.String())
		}
		loginRec := doJSON(t, srv, http.MethodPost, "/api/admins/login", authRequest{Email: "future-admin@example.com", Password: "new-admin-password-1"})
		if loginRec.Code == http.StatusOK {
			t.Fatalf("demoted account should no longer have an admin login")
		}
	})
}
