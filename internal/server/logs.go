package server

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"

	"onebox/internal/auth"
)

// logsCap bounds the _logs table: insertLogEntry occasionally trims to
// the newest logsCap rows instead of running a DELETE on every request.
const logsCap = 2000

type logEntry struct {
	ID         string `json:"id"`
	Time       string `json:"time"`
	Method     string `json:"method"`
	Path       string `json:"path"`
	Status     int    `json:"status"`
	UserID     string `json:"user_id,omitempty"`
	DurationMS int64  `json:"duration_ms"`
}

// requestLogger records every /api request to _logs for the admin Logs
// page. It reads the bearer token itself (rather than relying on
// downstream auth middleware to populate the request context, which
// varies per route and wouldn't be visible back here) so the identity
// column is populated regardless of which auth middleware, if any, a
// given route uses.
func (s *Server) requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		userID := ""
		if tok := bearerToken(r); tok != "" {
			if claims, err := auth.ParseToken(s.cfg.JWTSecret, tok); err == nil {
				userID = claims.Subject
			}
		}

		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)

		method, path, status := r.Method, r.URL.Path, ww.Status()
		dur := time.Since(start).Milliseconds()
		go insertLogEntry(context.Background(), s.db, method, path, status, userID, dur)
	})
}

func insertLogEntry(ctx context.Context, sqlDB *sql.DB, method, path string, status int, userID string, durationMS int64) {
	_, err := sqlDB.ExecContext(ctx,
		`INSERT INTO _logs (id, method, path, status, user_id, duration_ms) VALUES (?, ?, ?, ?, ?, ?)`,
		uuid.NewString(), method, path, status, nullableString(userID), durationMS,
	)
	if err != nil {
		log.Printf("insert log entry: %v", err)
		return
	}
	// Trim occasionally rather than on every insert — keeps this cheap
	// while still bounding table growth.
	if rand.Intn(50) == 0 {
		_, err := sqlDB.ExecContext(ctx,
			`DELETE FROM _logs WHERE id NOT IN (SELECT id FROM _logs ORDER BY time DESC, id DESC LIMIT ?)`,
			logsCap,
		)
		if err != nil {
			log.Printf("trim logs: %v", err)
		}
	}
}

// listLogs returns the most recent maxLogRows entries, newest first,
// optionally filtered by exact status or a path substring.
const maxLogRows = 500

func listLogs(ctx context.Context, sqlDB *sql.DB, status int, pathContains string) ([]logEntry, error) {
	var where []string
	var args []any
	if status != 0 {
		where = append(where, "status = ?")
		args = append(args, status)
	}
	if pathContains != "" {
		where = append(where, "path LIKE ?")
		args = append(args, "%"+strings.ReplaceAll(pathContains, "%", "")+"%")
	}

	stmt := `SELECT id, time, method, path, status, user_id, duration_ms FROM _logs`
	if len(where) > 0 {
		stmt += " WHERE " + strings.Join(where, " AND ")
	}
	stmt += " ORDER BY time DESC, id DESC LIMIT ?"
	args = append(args, maxLogRows)

	rows, err := sqlDB.QueryContext(ctx, stmt, args...)
	if err != nil {
		return nil, fmt.Errorf("query logs: %w", err)
	}
	defer rows.Close()

	out := []logEntry{}
	for rows.Next() {
		var e logEntry
		var uid sql.NullString
		if err := rows.Scan(&e.ID, &e.Time, &e.Method, &e.Path, &e.Status, &uid, &e.DurationMS); err != nil {
			return nil, fmt.Errorf("scan log row: %w", err)
		}
		e.UserID = uid.String
		out = append(out, e)
	}
	return out, rows.Err()
}
