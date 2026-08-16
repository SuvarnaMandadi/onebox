// diagnostics_history.go persists and reads back _provider_diagnostics
// rows — Section 7's Connection History and the data
// performanceSummary (Section 5) is computed from. See migration
// 0013_provider_diagnostics.sql for why the stored row is small.
package server

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
)

const diagnosticsHistoryLimit = 200

// recordDiagnosticsRun inserts one Connection History row. Called at the
// end of every diagnostics run (handleProviderDiagnostics), success or
// failure — a failed run is exactly what Connection History exists to
// show.
func recordDiagnosticsRun(ctx context.Context, sqlDB *sql.DB, report providerDiagnosticsReport) error {
	success := report.Status == severityOK
	summary := report.Version
	if !success && len(report.Issues) > 0 {
		summary = report.Issues[0].Summary
	} else if success {
		summary = "Connected successfully"
		if report.SelectedModel != "" {
			summary = fmt.Sprintf("Connected — %s", report.SelectedModel)
		}
	}
	_, err := sqlDB.ExecContext(ctx,
		`INSERT INTO _provider_diagnostics (id, provider, success, summary, duration_ms) VALUES (?, ?, ?, ?, ?)`,
		"diag_"+uuid.NewString(), report.Provider, boolToInt(success), summary, report.TotalCheckMS,
	)
	return err
}

// listDiagnosticsHistory returns the most recent runs, newest first,
// optionally filtered to one provider ("" means every provider).
func listDiagnosticsHistory(ctx context.Context, sqlDB *sql.DB, provider string, limit int) ([]diagnosticsHistoryEntry, error) {
	if limit <= 0 || limit > diagnosticsHistoryLimit {
		limit = diagnosticsHistoryLimit
	}
	query := `SELECT id, provider, success, summary, duration_ms, created FROM _provider_diagnostics`
	args := []any{}
	if provider != "" {
		query += ` WHERE provider = ?`
		args = append(args, provider)
	}
	// rowid DESC is the tiebreaker: created's millisecond-precision
	// timestamp can genuinely collide between two runs recorded in quick
	// succession (a real bug this exact ordering caught in testing — two
	// diagnostics runs issued back-to-back landed in the same
	// millisecond, making "newest first" non-deterministic on ties).
	// SQLite's implicit rowid strictly increases with insertion order on
	// an ordinary rowid table like this one, so it's a free, always-
	// correct secondary sort key — no schema change needed.
	query += ` ORDER BY created DESC, rowid DESC LIMIT ?`
	args = append(args, limit)

	rows, err := sqlDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query diagnostics history: %w", err)
	}
	defer rows.Close()

	// Initialized non-nil so a provider with zero recorded runs (every
	// provider on a fresh install) serializes as "items":[] rather than
	// "items":null — matching validateSettingsStructural's issues and
	// providerDiagnosticsReport.normalizeSlices' reasoning: the frontend
	// always expects an array here, and nil vs. empty carries no extra
	// meaning for "how many history rows exist."
	out := []diagnosticsHistoryEntry{}
	for rows.Next() {
		var e diagnosticsHistoryEntry
		var success int
		if err := rows.Scan(&e.ID, &e.Provider, &success, &e.Summary, &e.DurationMS, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan diagnostics history row: %w", err)
		}
		e.Success = success != 0
		out = append(out, e)
	}
	return out, rows.Err()
}

// computePerformanceSummary folds a provider's recent history into
// Section 5's Performance Panel. sampleSize bounds how many of the most
// recent runs feed the average/success-rate — a large historical average
// would smooth over a real, recent regression.
const performanceSampleSize = 20

func computePerformanceSummary(ctx context.Context, sqlDB *sql.DB, provider string) (performanceSummary, error) {
	entries, err := listDiagnosticsHistory(ctx, sqlDB, provider, performanceSampleSize)
	if err != nil {
		return performanceSummary{}, err
	}
	summary := performanceSummary{Provider: provider, SampleCount: len(entries)}
	if len(entries) == 0 {
		return summary, nil
	}

	var totalMS int64
	var successCount int
	for _, e := range entries {
		totalMS += e.DurationMS
		if e.Success {
			successCount++
			if summary.LastSuccessAt == "" {
				summary.LastSuccessAt = e.CreatedAt
			}
		} else if summary.LastFailureAt == "" {
			summary.LastFailureAt = e.CreatedAt
		}
	}
	avg := float64(totalMS) / float64(len(entries))
	summary.AvgResponseMS = &avg
	lastLatency := entries[0].DurationMS
	summary.LastLatencyMS = &lastLatency
	rate := float64(successCount) / float64(len(entries)) * 100
	summary.SuccessRatePercent = &rate

	summary.HealthScore, summary.Warnings = computeHealthScore(rate, avg, entries)
	return summary, nil
}

// computeHealthScore derives a 0-100 score purely from this provider's
// own observed history — success rate is the dominant factor (a
// consistently-failing provider cannot score well no matter how fast its
// few successes were), with a latency penalty only applied once there's
// enough of a baseline to judge "slow" against (the provider's own
// median, not a fixed number every provider is compared to — Ollama on a
// laptop CPU and a hosted API have very different honest baselines).
// Never a made-up or hardcoded value: a fresh provider with zero history
// is handled by the caller (computePerformanceSummary returns before
// reaching here), not by defaulting this function to a fake 100.
func computeHealthScore(successRatePercent, avgMS float64, entries []diagnosticsHistoryEntry) (int, []string) {
	score := successRatePercent
	var warnings []string

	if successRatePercent < 100 {
		warnings = append(warnings, fmt.Sprintf("%.0f%% of recent checks failed", 100-successRatePercent))
	}

	if len(entries) >= 3 {
		var latencies []int64
		for _, e := range entries {
			if e.Success {
				latencies = append(latencies, e.DurationMS)
			}
		}
		if len(latencies) >= 3 {
			median := medianInt64(latencies)
			if avgMS > float64(median)*2 && median > 0 {
				score -= 10
				warnings = append(warnings, "recent latency is notably higher than this provider's own recent median")
			}
		}
	}

	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}
	return int(score), warnings
}

func medianInt64(vals []int64) int64 {
	sorted := append([]int64(nil), vals...)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j-1] > sorted[j]; j-- {
			sorted[j-1], sorted[j] = sorted[j], sorted[j-1]
		}
	}
	return sorted[len(sorted)/2]
}
