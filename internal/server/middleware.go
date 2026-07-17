package server

import (
	"context"
	"net/http"
	"strings"

	"onebox/internal/auth"
)

type ctxKey string

const ctxKeyAuthUserID ctxKey = "auth_user_id"

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
