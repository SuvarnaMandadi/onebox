package server

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"onebox/internal/auth"
)

// TestUnifiedLoginAsSuperuser and TestUnifiedLoginAsUser cover "Login as
// Superuser" / "Login as User" through the one endpoint the dashboard's
// single login page actually calls (POST /api/login) — not the
// admin-only or user-only endpoints directly, which is what the earlier,
// role-toggle login page used.
func TestUnifiedLoginAsSuperuser(t *testing.T) {
	srv, _ := newTestServer(t)
	doJSON(t, srv, http.MethodPost, "/api/admins/signup", authRequest{Email: "root@example.com", Password: "hunter22222"})

	rec := doJSON(t, srv, http.MethodPost, "/api/login", authRequest{Email: "root@example.com", Password: "hunter22222"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	var resp unifiedLoginResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Role != "admin" {
		t.Fatalf("role = %q, want %q", resp.Role, "admin")
	}

	// the returned token must actually carry admin privileges
	adminRec := doAuth(t, srv, http.MethodGet, "/api/collections", resp.Token, nil)
	if adminRec.Code != http.StatusOK {
		t.Fatalf("admin-only endpoint with unified-login token: status = %d, want 200, body = %s", adminRec.Code, adminRec.Body.String())
	}
}

func TestUnifiedLoginAsUser(t *testing.T) {
	srv, _ := newTestServer(t)
	bootstrapAdmin(t, srv) // an admin must exist for the instance to be usable
	doJSON(t, srv, http.MethodPost, "/api/auth/signup", authRequest{Email: "user@example.com", Password: "hunter22222"})

	rec := doJSON(t, srv, http.MethodPost, "/api/login", authRequest{Email: "user@example.com", Password: "hunter22222"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	var resp unifiedLoginResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Role != "user" {
		t.Fatalf("role = %q, want %q", resp.Role, "user")
	}

	// a user token must work on user routes...
	meRec := doAuth(t, srv, http.MethodGet, "/api/auth/me", resp.Token, nil)
	if meRec.Code != http.StatusOK {
		t.Fatalf("/api/auth/me with user token: status = %d, want 200, body = %s", meRec.Code, meRec.Body.String())
	}
	// ...and must never work on admin routes.
	adminRec := doAuth(t, srv, http.MethodGet, "/api/collections", resp.Token, nil)
	if adminRec.Code != http.StatusUnauthorized {
		t.Fatalf("admin-only endpoint with user token: status = %d, want 401, body = %s", adminRec.Code, adminRec.Body.String())
	}
}

// TestUnifiedLoginPrioritizesAdminOverUser documents and locks in the
// deliberate tie-break for the edge case where the same email address
// exists in both _admins and _users (nothing stops that — the two tables
// have independent uniqueness constraints): the unified endpoint must
// authenticate against the admin record, using the admin's password, not
// silently fall through to the user record.
func TestUnifiedLoginPrioritizesAdminOverUser(t *testing.T) {
	srv, _ := newTestServer(t)
	doJSON(t, srv, http.MethodPost, "/api/admins/signup", authRequest{Email: "shared@example.com", Password: "admin-password1"})
	doJSON(t, srv, http.MethodPost, "/api/auth/signup", authRequest{Email: "shared@example.com", Password: "user-password1"})

	rec := doJSON(t, srv, http.MethodPost, "/api/login", authRequest{Email: "shared@example.com", Password: "admin-password1"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	var resp unifiedLoginResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Role != "admin" {
		t.Fatalf("role = %q, want %q (admin record should win on a shared email)", resp.Role, "admin")
	}

	// The user record's own password must not authenticate the (shared)
	// email through this endpoint, since it resolves to the admin record.
	wrongRec := doJSON(t, srv, http.MethodPost, "/api/login", authRequest{Email: "shared@example.com", Password: "user-password1"})
	if wrongRec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (user password against the shadowed admin record)", wrongRec.Code)
	}
}

func TestUnifiedLoginInvalidCredentials(t *testing.T) {
	srv, _ := newTestServer(t)
	bootstrapAdmin(t, srv)
	doJSON(t, srv, http.MethodPost, "/api/auth/signup", authRequest{Email: "user@example.com", Password: "hunter22222"})

	tests := []struct {
		name  string
		email string
		pass  string
	}{
		{"wrong admin password", "admin@example.com", "wrong-password"},
		{"wrong user password", "user@example.com", "wrong-password"},
		{"unknown email", "nobody@example.com", "irrelevant1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := doJSON(t, srv, http.MethodPost, "/api/login", authRequest{Email: tt.email, Password: tt.pass})
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401, body = %s", rec.Code, rec.Body.String())
			}
		})
	}
}

// TestRegistrationCanBeDisabled covers requirement 5: user registration
// must be toggleable from Settings, defaulting to enabled so existing
// deployments aren't silently locked out.
func TestRegistrationCanBeDisabled(t *testing.T) {
	srv, _ := newTestServer(t)
	token := bootstrapAdmin(t, srv)

	// default (no setting saved yet) — registration stays enabled
	openRec := doJSON(t, srv, http.MethodPost, "/api/auth/signup", authRequest{Email: "first@example.com", Password: "hunter22222"})
	if openRec.Code != http.StatusCreated {
		t.Fatalf("signup with default settings: status = %d, want 201, body = %s", openRec.Code, openRec.Body.String())
	}

	disableRec := doAuth(t, srv, http.MethodPut, "/api/settings", token, map[string]string{"registration_enabled": "false"})
	if disableRec.Code != http.StatusNoContent {
		t.Fatalf("disable registration: status = %d, want 204, body = %s", disableRec.Code, disableRec.Body.String())
	}

	blockedRec := doJSON(t, srv, http.MethodPost, "/api/auth/signup", authRequest{Email: "second@example.com", Password: "hunter22222"})
	if blockedRec.Code != http.StatusForbidden {
		t.Fatalf("signup while disabled: status = %d, want 403, body = %s", blockedRec.Code, blockedRec.Body.String())
	}
	var env errorEnvelope
	if err := json.Unmarshal(blockedRec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if env.Code != "registration_disabled" {
		t.Fatalf("code = %q, want %q", env.Code, "registration_disabled")
	}

	enableRec := doAuth(t, srv, http.MethodPut, "/api/settings", token, map[string]string{"registration_enabled": "true"})
	if enableRec.Code != http.StatusNoContent {
		t.Fatalf("re-enable registration: status = %d, want 204, body = %s", enableRec.Code, enableRec.Body.String())
	}
	reopenedRec := doJSON(t, srv, http.MethodPost, "/api/auth/signup", authRequest{Email: "third@example.com", Password: "hunter22222"})
	if reopenedRec.Code != http.StatusCreated {
		t.Fatalf("signup after re-enabling: status = %d, want 201, body = %s", reopenedRec.Code, reopenedRec.Body.String())
	}
}

// TestExpiredSessionRejected covers "session expiry": a syntactically
// valid, correctly-signed token whose exp claim is already in the past
// must be rejected exactly like an invalid one, on both the unified
// dashboard flow's downstream admin routes and plain user routes.
func TestExpiredSessionRejected(t *testing.T) {
	srv, _ := newTestServer(t)
	const secret = "test-secret"

	expiredAdmin := signExpiredToken(t, secret, "admin-1", auth.SubjectAdmin)
	rec := doAuth(t, srv, http.MethodGet, "/api/collections", expiredAdmin, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expired admin token: status = %d, want 401, body = %s", rec.Code, rec.Body.String())
	}

	expiredUser := signExpiredToken(t, secret, "user-1", auth.SubjectUser)
	rec = doAuth(t, srv, http.MethodGet, "/api/auth/me", expiredUser, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expired user token: status = %d, want 401, body = %s", rec.Code, rec.Body.String())
	}
}

// TestValidTokenSurvivesRepeatedRequests covers "refresh": onebox issues
// one long-lived token rather than a rotating refresh-token pair (see
// defaultTTL in internal/auth/jwt.go), so "refreshing" is simply that the
// same stored token keeps authenticating the same way on every request —
// reloading the dashboard must not require a new login.
func TestValidTokenSurvivesRepeatedRequests(t *testing.T) {
	srv, _ := newTestServer(t)
	token := bootstrapAdmin(t, srv)

	for i := 0; i < 3; i++ {
		rec := doAuth(t, srv, http.MethodGet, "/api/collections", token, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want 200, body = %s", i, rec.Code, rec.Body.String())
		}
	}
}

// signExpiredToken builds a JWT with the same shape auth.IssueToken
// produces, but with an exp claim already in the past — auth.IssueToken
// itself only ever issues forward-dated tokens, so an expired one can
// only be constructed directly against the exported Claims type.
func signExpiredToken(t *testing.T, secret, subjectID string, subjectType auth.SubjectType) string {
	t.Helper()
	past := time.Now().Add(-1 * time.Hour)
	claims := auth.Claims{
		Type: subjectType,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   subjectID,
			IssuedAt:  jwt.NewNumericDate(past.Add(-time.Hour)),
			ExpiresAt: jwt.NewNumericDate(past),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("sign expired token: %v", err)
	}
	return signed
}
