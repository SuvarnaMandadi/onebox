package server

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"onebox/internal/config"
	"onebox/internal/db"
)

func newTestServer(t *testing.T) (*Server, *sql.DB) {
	t.Helper()
	sqlDB, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.Migrate(sqlDB); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })

	cfg := config.Config{JWTSecret: "test-secret"}
	return New(cfg, sqlDB), sqlDB
}

func doJSON(t *testing.T, srv *Server, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	return rec
}

func TestSignup(t *testing.T) {
	tests := []struct {
		name       string
		body       authRequest
		seedEmail  string
		wantStatus int
		wantCode   string
	}{
		{
			name:       "valid signup",
			body:       authRequest{Email: "new@example.com", Password: "hunter22222"},
			wantStatus: http.StatusCreated,
		},
		{
			name:       "invalid email",
			body:       authRequest{Email: "not-an-email", Password: "hunter22222"},
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_email",
		},
		{
			name:       "short password",
			body:       authRequest{Email: "short@example.com", Password: "abc"},
			wantStatus: http.StatusBadRequest,
			wantCode:   "weak_password",
		},
		{
			name:       "duplicate email",
			body:       authRequest{Email: "dupe@example.com", Password: "hunter22222"},
			seedEmail:  "dupe@example.com",
			wantStatus: http.StatusConflict,
			wantCode:   "email_taken",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, sqlDB := newTestServer(t)
			if tt.seedEmail != "" {
				if _, err := createUser(t.Context(), sqlDB, tt.seedEmail, "irrelevant-hash", "", "", ""); err != nil {
					t.Fatalf("seed user: %v", err)
				}
			}

			rec := doJSON(t, srv, http.MethodPost, "/api/auth/signup", tt.body)

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
			} else {
				var resp authResponse
				if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
					t.Fatalf("decode response: %v", err)
				}
				if resp.Token == "" {
					t.Fatal("expected non-empty token")
				}
				if resp.Record == nil || resp.Record.Email != tt.body.Email {
					t.Fatalf("record = %+v, want email %q", resp.Record, tt.body.Email)
				}
			}
		})
	}
}

// TestSignupMixedCaseEmailIsSameAccount guards against a real user-reported
// bug: signing up with two different casings of the same email must not
// create two accounts.
func TestSignupMixedCaseEmailIsSameAccount(t *testing.T) {
	srv, _ := newTestServer(t)

	first := doJSON(t, srv, http.MethodPost, "/api/auth/signup", authRequest{Email: "Suvarna@Example.com", Password: "hunter22222"})
	if first.Code != http.StatusCreated {
		t.Fatalf("first signup failed: status = %d, body = %s", first.Code, first.Body.String())
	}

	second := doJSON(t, srv, http.MethodPost, "/api/auth/signup", authRequest{Email: "suvarna@example.com", Password: "hunter22222"})
	if second.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (same account under a different case), body = %s", second.Code, second.Body.String())
	}
	var env errorEnvelope
	if err := json.Unmarshal(second.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if env.Code != "email_taken" {
		t.Fatalf("code = %q, want %q", env.Code, "email_taken")
	}
}

func TestLogin(t *testing.T) {
	const seedEmail = "user@example.com"
	const seedPassword = "correct-password"

	tests := []struct {
		name       string
		email      string
		password   string
		wantStatus int
	}{
		{name: "correct credentials", email: seedEmail, password: seedPassword, wantStatus: http.StatusOK},
		{name: "wrong password", email: seedEmail, password: "wrong-password", wantStatus: http.StatusUnauthorized},
		{name: "unknown email", email: "nobody@example.com", password: seedPassword, wantStatus: http.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, _ := newTestServer(t)

			signupRec := doJSON(t, srv, http.MethodPost, "/api/auth/signup", authRequest{Email: seedEmail, Password: seedPassword})
			if signupRec.Code != http.StatusCreated {
				t.Fatalf("seed signup failed: status = %d, body = %s", signupRec.Code, signupRec.Body.String())
			}

			rec := doJSON(t, srv, http.MethodPost, "/api/auth/login", authRequest{Email: tt.email, Password: tt.password})

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d, body = %s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if tt.wantStatus == http.StatusOK {
				var resp authResponse
				if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
					t.Fatalf("decode response: %v", err)
				}
				if resp.Token == "" {
					t.Fatal("expected non-empty token")
				}
			}
		})
	}
}

// TestLoginMixedCaseEmail guards against a real user-reported bug: signing
// up as "Suvarna@X.com" and logging in as "suvarna@x.com" (or any other
// casing) must succeed — email identity is case-insensitive everywhere.
func TestLoginMixedCaseEmail(t *testing.T) {
	tests := []struct {
		name        string
		signupEmail string
		loginEmail  string
	}{
		{name: "login lowercase after mixed-case signup", signupEmail: "Suvarna@X.com", loginEmail: "suvarna@x.com"},
		{name: "login mixed-case after lowercase signup", signupEmail: "suvarna@x.com", loginEmail: "Suvarna@X.com"},
		{name: "login uppercase after mixed-case signup", signupEmail: "Suvarna@X.com", loginEmail: "SUVARNA@X.COM"},
		{name: "login with incidental whitespace", signupEmail: "suvarna@x.com", loginEmail: "  Suvarna@X.com  "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, _ := newTestServer(t)

			signupRec := doJSON(t, srv, http.MethodPost, "/api/auth/signup", authRequest{Email: tt.signupEmail, Password: "hunter22222"})
			if signupRec.Code != http.StatusCreated {
				t.Fatalf("signup failed: status = %d, body = %s", signupRec.Code, signupRec.Body.String())
			}

			loginRec := doJSON(t, srv, http.MethodPost, "/api/auth/login", authRequest{Email: tt.loginEmail, Password: "hunter22222"})
			if loginRec.Code != http.StatusOK {
				t.Fatalf("login status = %d, want 200, body = %s", loginRec.Code, loginRec.Body.String())
			}
			var resp authResponse
			if err := json.Unmarshal(loginRec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if resp.Token == "" {
				t.Fatal("expected non-empty token")
			}
		})
	}
}
