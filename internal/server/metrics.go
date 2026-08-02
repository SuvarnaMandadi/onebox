// metrics.go backs the admin dashboard's Logs page (rebuilt from a plain
// text log viewer into a monitoring dashboard — see renderLogs in app.js)
// with time-bucketed request/error/token series computed from the
// existing _logs and _usage tables, plus live process stats. No new
// storage: everything here is either a read of data already being
// collected (requestLogger/logUsage) or a point-in-time snapshot
// (runtime.MemStats, s.activeStreams).
package server

import (
	"context"
	"database/sql"
	"net/http"
	"runtime"
	"time"
)

// dashboardMetricsWindowMinutes is the fixed lookback window for every
// time-series below — one point per minute, so 60 points per series. The
// _logs table's 2000-row cap (see logsCap, logs.go) means a very
// high-traffic instance could lose history within this window before it's
// queried; acceptable for a "what's happening right now" dashboard, which
// is what this is for (the Usage page already covers longer-range,
// unbucketed history).
const dashboardMetricsWindowMinutes = 60

// bucketTimeFormat matches the granularity strftime('%Y-%m-%dT%H:%M:00Z', ...)
// produces server-side (seconds zeroed) — used both to build the SQL
// GROUP BY key and to generate the dense zero-filled series in Go, so a
// map lookup by formatted string always hits.
const bucketTimeFormat = "2006-01-02T15:04:00Z"

// logTimeFormat matches _logs.time/_usage.created's stored format (see
// the migrations' strftime('%Y-%m-%dT%H:%M:%fZ', 'now') default) closely
// enough for string comparison/ordering to behave like time comparison —
// same reasoning listLogs/listUsage's from/to filters already rely on.
const logTimeFormat = "2006-01-02T15:04:05.000Z"

type timeBucket struct {
	T string  `json:"t"`
	V float64 `json:"v"`
}

type processMetrics struct {
	Goroutines int `json:"goroutines"`
	// HeapAllocBytes/HeapSysBytes are Go's own memory stats (runtime.MemStats)
	// — the process's actual RAM use, not a host-wide figure (onebox is a
	// single binary with nothing else meaningfully sharing its process).
	HeapAllocBytes uint64 `json:"heap_alloc_bytes"`
	HeapSysBytes   uint64 `json:"heap_sys_bytes"`
	// GCCPUFraction is the fraction of this process's available CPU time
	// spent in garbage collection since start (runtime.MemStats field,
	// stdlib-only, works identically on every OS onebox runs on). Reported
	// as-is rather than as a stand-in for whole-system CPU% — true OS-level
	// CPU sampling needs per-platform syscalls (no such dependency exists
	// in this project today; see the "process" section of the dashboard
	// for why this is labeled "GC CPU," not "CPU"), and showing a made-up
	// number on a monitoring page would be worse than not showing one.
	GCCPUFraction float64 `json:"gc_cpu_fraction"`
	UptimeSeconds float64 `json:"uptime_seconds"`
}

func currentProcessMetrics(startedAt time.Time) processMetrics {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return processMetrics{
		Goroutines:     runtime.NumGoroutine(),
		HeapAllocBytes: m.HeapAlloc,
		HeapSysBytes:   m.HeapSys,
		GCCPUFraction:  m.GCCPUFraction,
		UptimeSeconds:  time.Since(startedAt).Seconds(),
	}
}

type dashboardMetricsResponse struct {
	GeneratedAt             string         `json:"generated_at"`
	WindowMinutes           int            `json:"window_minutes"`
	RequestsPerMinute       []timeBucket   `json:"requests_per_minute"`
	AvgResponseMSPerMinute  []timeBucket   `json:"avg_response_ms_per_minute"`
	ErrorsPerMinute         []timeBucket   `json:"errors_per_minute"`
	TokensPerMinute         []timeBucket   `json:"tokens_per_minute"`
	StreamingRequestsActive int64          `json:"streaming_requests_active"`
	ActiveUsersRecent       int            `json:"active_users_recent"`
	TotalRequestsWindow     int            `json:"total_requests_window"`
	TotalErrorsWindow       int            `json:"total_errors_window"`
	Process                 processMetrics `json:"process"`
}

// handleLogsMetrics is admin-only, powering the rebuilt Logs dashboard.
func (s *Server) handleLogsMetrics(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := time.Now().UTC()
	cutoff := now.Add(-dashboardMetricsWindowMinutes * time.Minute)
	cutoffStr := cutoff.Format(logTimeFormat)

	reqBuckets, avgBuckets, errBuckets, totalReq, totalErr, err := queryLogBuckets(ctx, s.db, cutoffStr)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to compute request metrics", nil)
		return
	}
	tokenBuckets, err := queryTokenBuckets(ctx, s.db, cutoffStr)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to compute token metrics", nil)
		return
	}
	activeUsers, err := countRecentActiveUsers(ctx, s.db, cutoffStr)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to compute active users", nil)
		return
	}

	writeJSON(w, http.StatusOK, dashboardMetricsResponse{
		GeneratedAt:             now.Format(time.RFC3339),
		WindowMinutes:           dashboardMetricsWindowMinutes,
		RequestsPerMinute:       fillMinuteBuckets(reqBuckets, cutoff, now),
		AvgResponseMSPerMinute:  fillMinuteBuckets(avgBuckets, cutoff, now),
		ErrorsPerMinute:         fillMinuteBuckets(errBuckets, cutoff, now),
		TokensPerMinute:         fillMinuteBuckets(tokenBuckets, cutoff, now),
		StreamingRequestsActive: s.activeStreams.Load(),
		ActiveUsersRecent:       activeUsers,
		TotalRequestsWindow:     totalReq,
		TotalErrorsWindow:       totalErr,
		Process:                 currentProcessMetrics(s.startedAt),
	})
}

// queryLogBuckets returns per-minute request count, average response time,
// and error count (status >= 400) since cutoff, keyed by the same
// bucketTimeFormat string fillMinuteBuckets expects.
func queryLogBuckets(ctx context.Context, db *sql.DB, cutoff string) (reqBuckets, avgBuckets, errBuckets map[string]float64, totalReq, totalErr int, err error) {
	rows, err := db.QueryContext(ctx, `
		SELECT strftime('%Y-%m-%dT%H:%M:00Z', time) AS bucket,
		       COUNT(*),
		       AVG(duration_ms),
		       SUM(CASE WHEN status >= 400 THEN 1 ELSE 0 END)
		FROM _logs
		WHERE time >= ?
		GROUP BY bucket`, cutoff)
	if err != nil {
		return nil, nil, nil, 0, 0, err
	}
	defer rows.Close()

	reqBuckets = map[string]float64{}
	avgBuckets = map[string]float64{}
	errBuckets = map[string]float64{}
	for rows.Next() {
		var bucket string
		var count int
		var avgDur float64
		var errCount int
		if scanErr := rows.Scan(&bucket, &count, &avgDur, &errCount); scanErr != nil {
			return nil, nil, nil, 0, 0, scanErr
		}
		reqBuckets[bucket] = float64(count)
		avgBuckets[bucket] = avgDur
		errBuckets[bucket] = float64(errCount)
		totalReq += count
		totalErr += errCount
	}
	return reqBuckets, avgBuckets, errBuckets, totalReq, totalErr, rows.Err()
}

// queryTokenBuckets sums tokens_in+tokens_out per minute from _usage —
// deliberately a different shape than the Usage page's existing "requests
// per day" bar chart (renderUsage/barChart, app.js), which counts calls,
// not tokens, and buckets by day, not minute.
func queryTokenBuckets(ctx context.Context, db *sql.DB, cutoff string) (map[string]float64, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT strftime('%Y-%m-%dT%H:%M:00Z', created) AS bucket, SUM(tokens_in + tokens_out)
		FROM _usage
		WHERE created >= ?
		GROUP BY bucket`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]float64{}
	for rows.Next() {
		var bucket string
		var tokens float64
		if scanErr := rows.Scan(&bucket, &tokens); scanErr != nil {
			return nil, scanErr
		}
		out[bucket] = tokens
	}
	return out, rows.Err()
}

func countRecentActiveUsers(ctx context.Context, db *sql.DB, cutoff string) (int, error) {
	var count int
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(DISTINCT user_id) FROM _logs WHERE time >= ? AND user_id IS NOT NULL`,
		cutoff,
	).Scan(&count)
	return count, err
}

// fillMinuteBuckets turns a sparse bucket->value map into a dense,
// gap-free series covering every minute from `from` to `to` inclusive —
// so a minute with zero requests renders as 0, not a break in the line.
func fillMinuteBuckets(data map[string]float64, from, to time.Time) []timeBucket {
	from = from.Truncate(time.Minute)
	to = to.Truncate(time.Minute)
	out := make([]timeBucket, 0, dashboardMetricsWindowMinutes+1)
	for t := from; !t.After(to); t = t.Add(time.Minute) {
		key := t.Format(bucketTimeFormat)
		out = append(out, timeBucket{T: key, V: data[key]})
	}
	return out
}
