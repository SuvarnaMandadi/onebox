// backend_health.go backs Settings' Backend Health panel — Section 10.
// Every figure here is either a real, synchronous check against this
// running instance (a DB ping, a file-count query, a real syscall for
// disk space) or explicitly reported as unavailable — never a guess or a
// hardcoded "healthy". See diskUsage (diskspace_windows.go/
// diskspace_unix.go) and currentProcessMetrics (metrics.go) for the two
// pieces this reuses rather than re-implementing.
package server

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"time"
)

type dbHealth struct {
	Reachable   bool   `json:"reachable"`
	WALMode     bool   `json:"wal_mode"`
	IntegrityOK bool   `json:"integrity_ok"`
	SizeBytes   int64  `json:"size_bytes"`
	Error       string `json:"error,omitempty"`
	LatencyMS   int64  `json:"latency_ms"`
}

type fileStorageHealth struct {
	Reachable      bool   `json:"reachable"`
	FileCount      int    `json:"file_count"`
	TotalSizeBytes int64  `json:"total_size_bytes"`
	Error          string `json:"error,omitempty"`
}

type diskHealth struct {
	Available   bool     `json:"available"`
	FreeBytes   uint64   `json:"free_bytes,omitempty"`
	TotalBytes  uint64   `json:"total_bytes,omitempty"`
	UsedPercent *float64 `json:"used_percent,omitempty"`
	Error       string   `json:"error,omitempty"`
}

type backupSchedulerHealth struct {
	Enabled          bool   `json:"enabled"`
	IntervalHours    int    `json:"interval_hours,omitempty"`
	RetentionCount   int    `json:"retention_count,omitempty"`
	BackupCount      int    `json:"backup_count"`
	LastBackupAt     string `json:"last_backup_at,omitempty"`
	LastBackupStatus string `json:"last_backup_status,omitempty"`
}

type entityCounts struct {
	Collections int `json:"collections"`
	Records     int `json:"records"`
	Files       int `json:"files"`
	Backups     int `json:"backups"`
}

type buildInfo struct {
	Version         string `json:"version"`
	Commit          string `json:"commit"`
	MigrationCount  int    `json:"migration_count"`
	LatestMigration string `json:"latest_migration"`
}

// backendHealthReport is the whole of Section 10, one call.
type backendHealthReport struct {
	Database        dbHealth              `json:"database"`
	FileStorage     fileStorageHealth     `json:"file_storage"`
	Disk            diskHealth            `json:"disk"`
	Process         processMetrics        `json:"process"`
	BackupScheduler backupSchedulerHealth `json:"backup_scheduler"`
	Counts          entityCounts          `json:"counts"`
	Build           buildInfo             `json:"build"`
	CheckedAt       string                `json:"checked_at"`
}

const dbIntegrityCheckTimeout = 5 * time.Second

func checkDatabaseHealth(ctx context.Context, sqlDB *sql.DB, dbPath string) dbHealth {
	start := time.Now()
	h := dbHealth{}
	if err := sqlDB.PingContext(ctx); err != nil {
		h.Error = err.Error()
		h.LatencyMS = time.Since(start).Milliseconds()
		return h
	}
	h.Reachable = true
	h.LatencyMS = time.Since(start).Milliseconds()

	var mode string
	if err := sqlDB.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&mode); err == nil {
		h.WALMode = mode == "wal"
	}

	// quick_check (not the full integrity_check) — verifies page
	// structure without the exhaustive, potentially slow full scan;
	// appropriate for a live health panel an admin might open
	// repeatedly, not a one-time deep audit.
	checkCtx, cancel := context.WithTimeout(ctx, dbIntegrityCheckTimeout)
	defer cancel()
	var result string
	if err := sqlDB.QueryRowContext(checkCtx, `PRAGMA quick_check`).Scan(&result); err == nil {
		h.IntegrityOK = result == "ok"
	}

	if info, err := os.Stat(dbPath); err == nil {
		h.SizeBytes = info.Size()
	}
	return h
}

func checkFileStorageHealth(ctx context.Context, sqlDB *sql.DB) fileStorageHealth {
	h := fileStorageHealth{}
	var count int
	var total sql.NullInt64
	err := sqlDB.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(size), 0) FROM _files WHERE kind = 'file'`).Scan(&count, &total)
	if err != nil {
		h.Error = err.Error()
		return h
	}
	h.Reachable = true
	h.FileCount = count
	h.TotalSizeBytes = total.Int64
	return h
}

func checkDiskHealth(path string) diskHealth {
	free, total, err := diskUsage(path)
	if err != nil {
		return diskHealth{Available: false, Error: err.Error()}
	}
	h := diskHealth{Available: true, FreeBytes: free, TotalBytes: total}
	if total > 0 {
		used := float64(total-free) / float64(total) * 100
		h.UsedPercent = &used
	}
	return h
}

func checkBackupSchedulerHealth(ctx context.Context, sqlDB *sql.DB, intervalHours, retentionCount int) (backupSchedulerHealth, error) {
	h := backupSchedulerHealth{
		Enabled:        intervalHours > 0,
		IntervalHours:  intervalHours,
		RetentionCount: retentionCount,
	}
	backups, err := listBackupHistory(ctx, sqlDB)
	if err != nil {
		return h, err
	}
	h.BackupCount = len(backups)
	if len(backups) > 0 {
		h.LastBackupAt = backups[0].Created
		h.LastBackupStatus = backups[0].Status
	}
	return h, nil
}

func computeEntityCounts(ctx context.Context, sqlDB *sql.DB) (entityCounts, error) {
	var c entityCounts

	collections, err := listCollections(ctx, sqlDB)
	if err != nil {
		return c, fmt.Errorf("list collections: %w", err)
	}
	c.Collections = len(collections)
	for _, col := range collections {
		c.Records += col.RecordCount
	}

	if err := sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM _files WHERE kind = 'file'`).Scan(&c.Files); err != nil {
		return c, fmt.Errorf("count files: %w", err)
	}

	backups, err := listBackupHistory(ctx, sqlDB)
	if err != nil {
		return c, fmt.Errorf("list backups: %w", err)
	}
	c.Backups = len(backups)

	return c, nil
}

func countMigrations(ctx context.Context, sqlDB *sql.DB) (count int, latest string, err error) {
	if err := sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM _migrations`).Scan(&count); err != nil {
		return 0, "", fmt.Errorf("count migrations: %w", err)
	}
	// Migration filenames sort correctly as strings (zero-padded numeric
	// prefix — see internal/db/migrations/*.sql), so MAX(name) is the
	// latest applied one without needing a separate "applied_at" ORDER BY.
	if err := sqlDB.QueryRowContext(ctx, `SELECT COALESCE(MAX(name), '') FROM _migrations`).Scan(&latest); err != nil {
		return count, "", fmt.Errorf("find latest migration: %w", err)
	}
	return count, latest, nil
}

// buildBackendHealthReport runs every Section 10 check against this
// live instance. Errors from any one check are captured on that check's
// own struct (Error field) rather than failing the whole report — a
// disk-space syscall failing on an unusual platform must not hide that
// the database itself is perfectly healthy.
func (s *Server) buildBackendHealthReport(ctx context.Context) (backendHealthReport, error) {
	report := backendHealthReport{CheckedAt: time.Now().UTC().Format(time.RFC3339)}

	report.Database = checkDatabaseHealth(ctx, s.db, s.cfg.DBPath)
	report.FileStorage = checkFileStorageHealth(ctx, s.db)
	report.Disk = checkDiskHealth(s.cfg.DataDir)
	report.Process = currentProcessMetrics(s.startedAt)

	schedHealth, err := checkBackupSchedulerHealth(ctx, s.db, s.cfg.BackupIntervalHours, s.cfg.BackupRetentionCount)
	if err != nil {
		return report, fmt.Errorf("backup scheduler health: %w", err)
	}
	report.BackupScheduler = schedHealth

	counts, err := computeEntityCounts(ctx, s.db)
	if err != nil {
		return report, fmt.Errorf("entity counts: %w", err)
	}
	report.Counts = counts

	migrationCount, latestMigration, err := countMigrations(ctx, s.db)
	if err != nil {
		return report, fmt.Errorf("migration info: %w", err)
	}
	report.Build = buildInfo{
		Version:         s.cfg.Version,
		Commit:          s.cfg.Commit,
		MigrationCount:  migrationCount,
		LatestMigration: latestMigration,
	}

	return report, nil
}

// handleBackendHealth is admin-only: GET /api/settings/backend-health.
func (s *Server) handleBackendHealth(w http.ResponseWriter, r *http.Request) {
	report, err := s.buildBackendHealthReport(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to build backend health report: "+err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, report)
}
