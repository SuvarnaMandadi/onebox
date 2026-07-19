package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAdminSignup(t *testing.T) {
	tests := []struct {
		name       string
		seedAdmin  bool
		body       authRequest
		wantStatus int
		wantCode   string
	}{
		{
			name:       "first admin bootstraps",
			body:       authRequest{Email: "root@example.com", Password: "hunter22222"},
			wantStatus: http.StatusCreated,
		},
		{
			name:       "second admin rejected once bootstrapped",
			seedAdmin:  true,
			body:       authRequest{Email: "second@example.com", Password: "hunter22222"},
			wantStatus: http.StatusForbidden,
			wantCode:   "setup_complete",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, _ := newTestServer(t)
			if tt.seedAdmin {
				rec := doJSON(t, srv, http.MethodPost, "/api/admins/signup", authRequest{Email: "root@example.com", Password: "hunter22222"})
				if rec.Code != http.StatusCreated {
					t.Fatalf("seed admin signup failed: status = %d, body = %s", rec.Code, rec.Body.String())
				}
			}

			rec := doJSON(t, srv, http.MethodPost, "/api/admins/signup", tt.body)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d, body = %s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if tt.wantCode != "" {
				var env errorEnvelope
				if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
					t.Fatalf("decode error body: %v", err)
				}
				if env.Code != tt.wantCode {
					t.Fatalf("code = %q, want %q", env.Code, tt.wantCode)
				}
			}
		})
	}
}

func TestAdminLogin(t *testing.T) {
	const seedEmail = "root@example.com"
	const seedPassword = "hunter22222"

	tests := []struct {
		name       string
		email      string
		password   string
		wantStatus int
	}{
		{name: "correct credentials", email: seedEmail, password: seedPassword, wantStatus: http.StatusOK},
		{name: "wrong password", email: seedEmail, password: "wrong-password", wantStatus: http.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, _ := newTestServer(t)
			seedRec := doJSON(t, srv, http.MethodPost, "/api/admins/signup", authRequest{Email: seedEmail, Password: seedPassword})
			if seedRec.Code != http.StatusCreated {
				t.Fatalf("seed admin signup failed: status = %d, body = %s", seedRec.Code, seedRec.Body.String())
			}

			rec := doJSON(t, srv, http.MethodPost, "/api/admins/login", authRequest{Email: tt.email, Password: tt.password})

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d, body = %s", rec.Code, tt.wantStatus, rec.Body.String())
			}
		})
	}
}

// TestAdminLoginMixedCaseEmail mirrors TestLoginMixedCaseEmail for admins —
// the same user-reported case-sensitivity bug applies to admin accounts.
func TestAdminLoginMixedCaseEmail(t *testing.T) {
	srv, _ := newTestServer(t)

	signupRec := doJSON(t, srv, http.MethodPost, "/api/admins/signup", authRequest{Email: "Root@Example.com", Password: "hunter22222"})
	if signupRec.Code != http.StatusCreated {
		t.Fatalf("signup failed: status = %d, body = %s", signupRec.Code, signupRec.Body.String())
	}

	loginRec := doJSON(t, srv, http.MethodPost, "/api/admins/login", authRequest{Email: "ROOT@EXAMPLE.COM", Password: "hunter22222"})
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login status = %d, want 200, body = %s", loginRec.Code, loginRec.Body.String())
	}
}

// TestLoginErrorMessagesDistinguishCause covers the hand-tested feedback
// that "invalid credentials" was shown even when no account exists at
// all — login (both _users and _admins) should say "no_account" for an
// unknown email and "invalid_credentials" only for a wrong password on an
// account that does exist.
func TestLoginErrorMessagesDistinguishCause(t *testing.T) {
	srv, _ := newTestServer(t)
	signupUser(t, srv, "known@example.com")

	t.Run("unknown user email", func(t *testing.T) {
		rec := doJSON(t, srv, http.MethodPost, "/api/auth/login", authRequest{Email: "nobody@example.com", Password: "irrelevant1"})
		assertErrorCode(t, rec, http.StatusUnauthorized, "no_account")
	})
	t.Run("known user email, wrong password", func(t *testing.T) {
		rec := doJSON(t, srv, http.MethodPost, "/api/auth/login", authRequest{Email: "known@example.com", Password: "wrong-password"})
		assertErrorCode(t, rec, http.StatusUnauthorized, "invalid_credentials")
	})

	bootstrapAdmin(t, srv)
	t.Run("unknown admin email", func(t *testing.T) {
		rec := doJSON(t, srv, http.MethodPost, "/api/admins/login", authRequest{Email: "nobody@example.com", Password: "irrelevant1"})
		assertErrorCode(t, rec, http.StatusUnauthorized, "no_account")
	})
	t.Run("known admin email, wrong password", func(t *testing.T) {
		rec := doJSON(t, srv, http.MethodPost, "/api/admins/login", authRequest{Email: "admin@example.com", Password: "wrong-password"})
		assertErrorCode(t, rec, http.StatusUnauthorized, "invalid_credentials")
	})
}

func assertErrorCode(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int, wantCode string) {
	t.Helper()
	if rec.Code != wantStatus {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, wantStatus, rec.Body.String())
	}
	var env errorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if env.Code != wantCode {
		t.Fatalf("code = %q, want %q, body = %s", env.Code, wantCode, rec.Body.String())
	}
}

func TestSetupStatus(t *testing.T) {
	srv, _ := newTestServer(t)

	rec := doJSON(t, srv, http.MethodGet, "/api/setup-status", nil)
	var before struct {
		AdminExists bool `json:"admin_exists"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &before); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if before.AdminExists {
		t.Fatalf("expected admin_exists = false before any admin signs up")
	}

	bootstrapAdmin(t, srv)

	rec = doJSON(t, srv, http.MethodGet, "/api/setup-status", nil)
	var after struct {
		AdminExists bool `json:"admin_exists"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &after); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !after.AdminExists {
		t.Fatalf("expected admin_exists = true after bootstrapping an admin")
	}
}
