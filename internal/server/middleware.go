package server

import (
	"context"
	"net/http"
	"strings"

	"onebox/internal/auth"
)

type ctxKey string

const (
	ctxKeyAuthUserID  ctxKey = "auth_user_id"
	ctxKeyAuthAdminID ctxKey = "auth_admin_id"
)

// requireUserAuth validates the Authorization: Bearer <jwt> header for a
// _users session and stores the subject id in the request context.
func (s *Server) requireUserAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenStr := bearerToken(r)
		if tokenStr == "" {
			writeError(w, http.StatusUnauthorized, "missing_token", "Authorization: Bearer <token> header is required", nil)
			return
		}

		claims, err := auth.ParseToken(s.cfg.JWTSecret, tokenStr)
		if err != nil || claims.Type != auth.SubjectUser {
			writeError(w, http.StatusUnauthorized, "invalid_token", "session token is invalid or expired", nil)
			return
		}

		ctx := context.WithValue(r.Context(), ctxKeyAuthUserID, claims.Subject)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// requireAdminAuth validates the Authorization: Bearer <jwt> header for an
// _admins session and stores the subject id in the request context.
func (s *Server) requireAdminAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenStr := bearerToken(r)
		if tokenStr == "" {
			writeError(w, http.StatusUnauthorized, "missing_token", "Authorization: Bearer <token> header is required", nil)
			return
		}

		claims, err := auth.ParseToken(s.cfg.JWTSecret, tokenStr)
		if err != nil || claims.Type != auth.SubjectAdmin {
			writeError(w, http.StatusUnauthorized, "invalid_token", "admin session token is invalid or expired", nil)
			return
		}

		ctx := context.WithValue(r.Context(), ctxKeyAuthAdminID, claims.Subject)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// requireAnyAuth accepts either a _users or _admins session token and
// stores whichever subject it found in the request context. Used for
// endpoints like file upload where any signed-in identity may act, but the
// specific rule (owner vs admin) is decided later per-resource.
func (s *Server) requireAnyAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenStr := bearerToken(r)
		if tokenStr == "" {
			writeError(w, http.StatusUnauthorized, "missing_token", "Authorization: Bearer <token> header is required", nil)
			return
		}

		claims, err := auth.ParseToken(s.cfg.JWTSecret, tokenStr)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid_token", "session token is invalid or expired", nil)
			return
		}

		var ctx context.Context
		switch claims.Type {
		case auth.SubjectUser:
			ctx = context.WithValue(r.Context(), ctxKeyAuthUserID, claims.Subject)
		case auth.SubjectAdmin:
			ctx = context.WithValue(r.Context(), ctxKeyAuthAdminID, claims.Subject)
		default:
			writeError(w, http.StatusUnauthorized, "invalid_token", "session token is invalid or expired", nil)
			return
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(h, prefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(h, prefix))
}

// authUserID returns the authenticated _users id from a request context
// populated by requireUserAuth, if any.
func authUserID(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(ctxKeyAuthUserID).(string)
	return id, ok
}

// authAdminID returns the authenticated _admins id from a request context
// populated by requireAdminAuth, if any.
func authAdminID(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(ctxKeyAuthAdminID).(string)
	return id, ok
}

// optionalAuth reads and validates a Bearer token if present, without
// rejecting the request when it's absent or invalid. Used by the records
// API, where routes can be public, authenticated-only, or owner-only
// depending on each collection's rules.
func (s *Server) optionalAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if tokenStr := bearerToken(r); tokenStr != "" {
			if claims, err := auth.ParseToken(s.cfg.JWTSecret, tokenStr); err == nil {
				switch claims.Type {
				case auth.SubjectUser:
					r = r.WithContext(context.WithValue(r.Context(), ctxKeyAuthUserID, claims.Subject))
				case auth.SubjectAdmin:
					r = r.WithContext(context.WithValue(r.Context(), ctxKeyAuthAdminID, claims.Subject))
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}
