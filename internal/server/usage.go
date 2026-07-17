package server

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
)

type usageRecord struct {
	ID           string  `json:"id"`
	UserID       string  `json:"user_id,omitempty"`
	Provider     string  `json:"provider"`
	Model        string  `json:"model"`
	TokensIn     int     `json:"tokens_in"`
	TokensOut    int     `json:"tokens_out"`
	CostEstimate float64 `json:"cost_estimate"`
	Cached       bool    `json:"cached"`
	Created      string  `json:"created"`
}

// modelPricing is USD per 1M tokens. Approximate, and only covers common
// hosted models — unrecognized models (including every Ollama model,
// which run locally for free) cost $0. Good enough for the "don't get
// surprised by a bill" spend dashboard v0.1 promises; exact pricing
// varies by provider and changes often enough that hardcoding precision
// here wouldn't hold up anyway.
type pricePer1M struct{ in, out float64 }

var modelPricing = map[string]pricePer1M{
	"claude-opus-4-8":        {in: 15, out: 75},
	"claude-sonnet-5":        {in: 3, out: 15},
	"claude-haiku-4-5":       {in: 0.8, out: 4},
	"gpt-4o":                 {in: 2.5, out: 10},
	"gpt-4o-mini":            {in: 0.15, out: 0.6},
	"text-embedding-3-small": {in: 0.02, out: 0},
	"text-embedding-3-large": {in: 0.13, out: 0},
}

func estimateCost(model string, tokensIn, tokensOut int) float64 {
	price, ok := modelPricing[model]
	if !ok {
		// Try a prefix match (e.g. "claude-sonnet-5-20260115" style
		// dated model names) before giving up and pricing it as free.
		for name, p := range modelPricing {
			if strings.HasPrefix(model, name) {
				price = p
				ok = true
				break
			}
		}
	}
	if !ok {
		return 0
	}
	return (float64(tokensIn)/1_000_000)*price.in + (float64(tokensOut)/1_000_000)*price.out
}

// logUsage records one LLM/embedding call. Failures are logged, not
// returned — usage logging must never break the response the caller is
// already waiting on.
func (s *Server) logUsage(ctx context.Context, provider, model string, tokensIn, tokensOut int, cached bool) {
	userID := billingID(ctx)
	cost := estimateCost(model, tokensIn, tokensOut)

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO _usage (id, user_id, provider, model, tokens_in, tokens_out, cost_estimate, cached) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		uuid.NewString(), nullableString(userID), provider, model, tokensIn, tokensOut, cost, boolToInt(cached),
	)
	if err != nil {
		log.Printf("log usage: %v", err)
	}
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// billingID is whichever identity (end-user or admin) a request is
// attributed to for rate limiting, spend caps, and usage logs.
func billingID(ctx context.Context) string {
	if uid, ok := authUserID(ctx); ok {
		return uid
	}
	if aid, ok := authAdminID(ctx); ok {
		return aid
	}
	return ""
}

// monthlySpend sums cost_estimate for userID since the first of the
// current month, in server local time.
func monthlySpend(ctx context.Context, sqlDB *sql.DB, userID string) (float64, error) {
	monthStart := time.Now().Format("2006-01") + "-01T00:00:00.000Z"
	var total float64
	err := sqlDB.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(cost_estimate), 0) FROM _usage WHERE user_id = ? AND created >= ?`,
		userID, monthStart,
	).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("sum monthly spend: %w", err)
	}
	return total, nil
}

const maxUsageRows = 500

// listUsage returns usage rows newest-first, optionally filtered by user
// and/or a [from, to) created range (both ISO8601, either may be empty).
func listUsage(ctx context.Context, sqlDB *sql.DB, userID, from, to string) ([]usageRecord, error) {
	var where []string
	var args []any
	if userID != "" {
		where = append(where, "user_id = ?")
		args = append(args, userID)
	}
	if from != "" {
		where = append(where, "created >= ?")
		args = append(args, from)
	}
	if to != "" {
		where = append(where, "created <= ?")
		args = append(args, to)
	}

	stmt := `SELECT id, user_id, provider, model, tokens_in, tokens_out, cost_estimate, cached, created FROM _usage`
	if len(where) > 0 {
		stmt += " WHERE " + strings.Join(where, " AND ")
	}
	stmt += " ORDER BY created DESC LIMIT ?"
	args = append(args, maxUsageRows)

	rows, err := sqlDB.QueryContext(ctx, stmt, args...)
	if err != nil {
		return nil, fmt.Errorf("query usage: %w", err)
	}
	defer rows.Close()

	out := []usageRecord{}
	for rows.Next() {
		var u usageRecord
		var uid sql.NullString
		var cached int
		if err := rows.Scan(&u.ID, &uid, &u.Provider, &u.Model, &u.TokensIn, &u.TokensOut, &u.CostEstimate, &cached, &u.Created); err != nil {
			return nil, fmt.Errorf("scan usage row: %w", err)
		}
		u.UserID = uid.String
		u.Cached = cached != 0
		out = append(out, u)
	}
	return out, rows.Err()
}
