package server

import (
	"encoding/json"
	"net/http"
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
