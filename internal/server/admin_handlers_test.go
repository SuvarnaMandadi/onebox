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

// TestLoginErrorMessagesDoNotDistinguishCause is the account-enumeration
// regression test (security-audit Fix 7): an unknown email and a wrong
// password on a known email used to return distinct codes ("no_account" vs
// "invalid_credentials"), which let a caller cheaply enumerate which
// emails have accounts by watching which error came back. Both cases must
// now return the exact same generic code/message — see invalidLoginCode's
// doc comment in auth_handlers.go. This intentionally replaces the old
// TestLoginErrorMessagesDistinguishCause, which pinned the opposite
// (distinguishing) behavior as correct.
func TestLoginErrorMessagesDoNotDistinguishCause(t *testing.T) {
	srv, _ := newTestServer(t)
	signupUser(t, srv, "known@example.com")

	t.Run("unknown user email", func(t *testing.T) {
		rec := doJSON(t, srv, http.MethodPost, "/api/auth/login", authRequest{Email: "nobody@example.com", Password: "irrelevant1"})
		assertErrorCode(t, rec, http.StatusUnauthorized, invalidLoginCode)
	})
	t.Run("known user email, wrong password", func(t *testing.T) {
		rec := doJSON(t, srv, http.MethodPost, "/api/auth/login", authRequest{Email: "known@example.com", Password: "wrong-password"})
		assertErrorCode(t, rec, http.StatusUnauthorized, invalidLoginCode)
	})

	bootstrapAdmin(t, srv)
	t.Run("unknown admin email", func(t *testing.T) {
		rec := doJSON(t, srv, http.MethodPost, "/api/admins/login", authRequest{Email: "nobody@example.com", Password: "irrelevant1"})
		assertErrorCode(t, rec, http.StatusUnauthorized, invalidLoginCode)
	})
	t.Run("known admin email, wrong password", func(t *testing.T) {
		rec := doJSON(t, srv, http.MethodPost, "/api/admins/login", authRequest{Email: "admin@example.com", Password: "wrong-password"})
		assertErrorCode(t, rec, http.StatusUnauthorized, invalidLoginCode)
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

// TestSuperuserHasAdminPrivileges verifies the token issued at admin
// bootstrap actually passes requireAdminAuth on an admin-only endpoint —
// distinct from TestAdminSignup, which only checks the signup response
// itself, not that the resulting session can do admin things.
func TestSuperuserHasAdminPrivileges(t *testing.T) {
	srv, _ := newTestServer(t)
	token := bootstrapAdmin(t, srv)

	rec := doAuth(t, srv, http.MethodGet, "/api/collections", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin-only endpoint status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
}

// TestNormalUserCannotAccessAdminEndpoints and
// TestUserLoginDoesNotGrantAdminAccess cover the flip side: a regular
// _users session — even a freshly logged-in one — must never pass
// requireAdminAuth. Users and admins are separate concepts end to end:
// separate tables (_users vs _admins), separate signup/login endpoints,
// separate JWT subject types (auth.SubjectUser vs auth.SubjectAdmin), and
// a _users session must never satisfy an admin-only route.
func TestNormalUserCannotAccessAdminEndpoints(t *testing.T) {
	srv, _ := newTestServer(t)
	bootstrapAdmin(t, srv) // an admin must exist for the instance to be usable at all
	_, userToken := signupUser(t, srv, "user@example.com")

	rec := doAuth(t, srv, http.MethodGet, "/api/collections", userToken, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401, body = %s", rec.Code, rec.Body.String())
	}
}

func TestUserLoginDoesNotGrantAdminAccess(t *testing.T) {
	srv, _ := newTestServer(t)
	bootstrapAdmin(t, srv)
	signupUser(t, srv, "user@example.com")

	loginRec := doJSON(t, srv, http.MethodPost, "/api/auth/login", authRequest{Email: "user@example.com", Password: "hunter22222"})
	if loginRec.Code != http.StatusOK {
		t.Fatalf("user login status = %d, want 200, body = %s", loginRec.Code, loginRec.Body.String())
	}
	var resp authResponse
	if err := json.Unmarshal(loginRec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode login response: %v", err)
	}

	rec := doAuth(t, srv, http.MethodGet, "/api/collections", resp.Token, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for a user token on an admin endpoint, body = %s", rec.Code, rec.Body.String())
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
