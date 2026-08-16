// RC3: backup history, scheduling, retention, and restore preview — the
// production-quality backup experience the RC2 audit flagged as the
// weakest product area (a backup only ever existed as a one-off streamed
// download, never persisted, never listed, never previewed before a
// destructive restore). Everything here reuses the exact snapshot/restore
// primitives backup_handlers.go's original handleExportBackup/
// handleImportBackup already proved out (VACUUM INTO for a consistent
// snapshot without pausing the live connection, ATTACH+DELETE/INSERT for
// merge-restore) — this file adds persistence, metadata, and a dry-run
// path around that same core, not a second backup mechanism.
package server

import (
	"archive/zip"
	"context"
	"database/sql"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"onebox/internal/config"
)

// backupMeta is one _backups row.
type backupMeta struct {
	ID          string `json:"id"`
	Filename    string `json:"filename"`
	SizeBytes   int64  `json:"size_bytes"`
	Trigger     string `json:"trigger"` // "manual" or "scheduled"
	Status      string `json:"status"`  // "complete" or "failed"
	TablesCount int    `json:"tables_count"`
	Error       string `json:"error,omitempty"`
	Created     string `json:"created"`
}

// backupsDir is where persisted backup .zips live — a sibling of FilesDir
// under DataDir, same split as uploaded file content vs. _files metadata.
func backupsDir(cfg config.Config) string {
	return filepath.Join(cfg.DataDir, "backups")
}

// createBackupSnapshot is the one place that actually produces a backup
// .zip — reused by the manual POST /api/backups handler and the
// scheduled ticker (StartBackupScheduler) alike, so "what a backup
// contains" only has one implementation. Writes the zip to backupsDir,
// records a _backups row (even on failure, so a failed attempt shows up
// in history instead of vanishing silently — RC3's "backup health"), and
// returns the metadata.
func createBackupSnapshot(ctx context.Context, s *Server, trigger string) (*backupMeta, error) {
	id := uuid.NewString()
	filename := "onebox-backup-" + time.Now().UTC().Format("2006-01-02-150405") + ".zip"

	meta, snapErr := writeBackupZip(ctx, s, filepath.Join(backupsDir(s.cfg), id+".zip"))

	m := &backupMeta{ID: id, Filename: filename, Trigger: trigger, Created: time.Now().UTC().Format(time.RFC3339)}
	if snapErr != nil {
		m.Status = "failed"
		m.Error = snapErr.Error()
	} else {
		m.Status = "complete"
		m.SizeBytes = meta.sizeBytes
		m.TablesCount = meta.tablesCount
	}

	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO _backups (id, filename, size_bytes, trigger, status, tables_count, error, created)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.Filename, m.SizeBytes, m.Trigger, m.Status, m.TablesCount, m.Error, m.Created,
	); err != nil {
		return nil, fmt.Errorf("record backup metadata: %w", err)
	}

	if snapErr != nil {
		return m, snapErr
	}
	return m, nil
}

type snapshotResult struct {
	sizeBytes   int64
	tablesCount int
}

// writeBackupZip does the actual snapshot work — VACUUM INTO a temp file
// (consistent, non-blocking, same as the original handleExportBackup),
// zip it alongside every stored file, and write the result to destPath.
// Split out from createBackupSnapshot so its error can be captured and
// recorded as a "failed" history row rather than the request just erroring
// out with nothing to show for it afterward.
func writeBackupZip(ctx context.Context, s *Server, destPath string) (snapshotResult, error) {
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return snapshotResult{}, fmt.Errorf("prepare backups directory: %w", err)
	}

	tmp, err := os.CreateTemp("", "onebox-backup-*.db")
	if err != nil {
		return snapshotResult{}, fmt.Errorf("prepare snapshot: %w", err)
	}
	tmpPath := tmp.Name()
	tmp.Close()
	os.Remove(tmpPath) // VACUUM INTO requires the target not exist yet
	defer os.Remove(tmpPath)

	if _, err := s.db.ExecContext(ctx, fmt.Sprintf("VACUUM INTO %q", tmpPath)); err != nil {
		return snapshotResult{}, fmt.Errorf("snapshot database: %w", err)
	}

	tablesCount, err := countTables(tmpPath)
	if err != nil {
		return snapshotResult{}, err
	}

	out, err := os.Create(destPath)
	if err != nil {
		return snapshotResult{}, fmt.Errorf("create backup file: %w", err)
	}
	defer out.Close()

	zw := zip.NewWriter(out)
	if err := addFileToZip(zw, tmpPath, "data.db"); err != nil {
		zw.Close()
		return snapshotResult{}, fmt.Errorf("write database to backup: %w", err)
	}
	filepath.Walk(s.cfg.FilesDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(s.cfg.FilesDir, path)
		if err != nil {
			return nil
		}
		addFileToZip(zw, path, "files/"+filepath.ToSlash(rel))
		return nil
	})
	if err := zw.Close(); err != nil {
		return snapshotResult{}, fmt.Errorf("finalize backup archive: %w", err)
	}

	info, err := out.Stat()
	if err != nil {
		// The archive is already written and closed successfully at this
		// point — a Stat failure here is cosmetic (size just reports 0),
		// not a reason to report the whole backup as failed.
		return snapshotResult{tablesCount: tablesCount}, nil
	}
	return snapshotResult{sizeBytes: info.Size(), tablesCount: tablesCount}, nil
}

// countTables opens the snapshot file briefly (a plain os/sql open, not
// through the live *sql.DB) just to count real tables — doubles as a
// cheap integrity check: VACUUM INTO having produced a file that SQLite
// itself can't open would be exactly the kind of silent corruption a
// backup history is supposed to catch, not paper over.
func countTables(path string) (int, error) {
	dsn := path + "?mode=ro"
	snapDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return 0, fmt.Errorf("open snapshot for verification: %w", err)
	}
	defer snapDB.Close()
	var n int
	if err := snapDB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`).Scan(&n); err != nil {
		return 0, fmt.Errorf("verify snapshot integrity: %w", err)
	}
	return n, nil
}

// listBackupHistory returns every _backups row, newest first.
func listBackupHistory(ctx context.Context, sqlDB *sql.DB) ([]backupMeta, error) {
	rows, err := sqlDB.QueryContext(ctx, `
		SELECT id, filename, size_bytes, trigger, status, tables_count, error, created
		FROM _backups ORDER BY created DESC`)
	if err != nil {
		return nil, fmt.Errorf("list backups: %w", err)
	}
	defer rows.Close()
	out := []backupMeta{}
	for rows.Next() {
		var m backupMeta
		if err := rows.Scan(&m.ID, &m.Filename, &m.SizeBytes, &m.Trigger, &m.Status, &m.TablesCount, &m.Error, &m.Created); err != nil {
			return nil, fmt.Errorf("scan backup row: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

var errBackupNotFound = fmt.Errorf("backup not found")

func getBackupMeta(ctx context.Context, sqlDB *sql.DB, id string) (*backupMeta, error) {
	var m backupMeta
	err := sqlDB.QueryRowContext(ctx, `
		SELECT id, filename, size_bytes, trigger, status, tables_count, error, created
		FROM _backups WHERE id = ?`, id,
	).Scan(&m.ID, &m.Filename, &m.SizeBytes, &m.Trigger, &m.Status, &m.TablesCount, &m.Error, &m.Created)
	if err == sql.ErrNoRows {
		return nil, errBackupNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load backup: %w", err)
	}
	return &m, nil
}

// deleteBackupByID removes both the metadata row and the on-disk file —
// the file is best-effort (a row whose file already vanished from disk
// should still be removable from history, not stuck forever).
func deleteBackupByID(ctx context.Context, sqlDB *sql.DB, cfg config.Config, id string) error {
	if _, err := getBackupMeta(ctx, sqlDB, id); err != nil {
		return err
	}
	os.Remove(filepath.Join(backupsDir(cfg), id+".zip"))
	if _, err := sqlDB.ExecContext(ctx, `DELETE FROM _backups WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete backup metadata: %w", err)
	}
	return nil
}

// enforceBackupRetention deletes the oldest backups beyond keep — called
// after every scheduled backup (never after a manual one on its own
// action; a manual backup can still be the one that gets pruned later,
// once it's no longer among the newest `keep`). keep <= 0 disables
// retention entirely (unbounded history).
func enforceBackupRetention(ctx context.Context, sqlDB *sql.DB, cfg config.Config, keep int) error {
	if keep <= 0 {
		return nil
	}
	rows, err := sqlDB.QueryContext(ctx, `SELECT id FROM _backups ORDER BY created DESC LIMIT -1 OFFSET ?`, keep)
	if err != nil {
		return fmt.Errorf("find backups over retention: %w", err)
	}
	var toDelete []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		toDelete = append(toDelete, id)
	}
	rows.Close()
	for _, id := range toDelete {
		if err := deleteBackupByID(ctx, sqlDB, cfg, id); err != nil {
			log.Printf("backup retention: failed to delete %s: %v", id, err)
		}
	}
	return nil
}

// restorePreview is dry-run output for a stored backup — RC3's "restore
// preview": what would happen, without touching any live data. Reuses
// the exact table-diffing ATTACH/DETACH pair restoreFromBackupDB already
// does for the real restore, just with no DELETE/INSERT and no COMMIT.
type restorePreview struct {
	TablesToRestore []restoreTablePreview `json:"tables_to_restore"`
	TablesSkipped   []string              `json:"tables_skipped"`
	FilesInBackup   int                   `json:"files_in_backup"`
}

type restoreTablePreview struct {
	Table          string `json:"table"`
	LiveRowCount   int    `json:"live_row_count"`
	BackupRowCount int    `json:"backup_row_count"`
}

// validateBackupZip checks a backup archive is genuinely restorable
// before either previewing or restoring from it — RC3's "backup
// validation": a corrupt zip, or a data.db VACUUM INTO or a hand-crafted
// upload never actually wrote correctly, fails loudly and specifically
// here rather than partway through a real restore's transaction.
func validateBackupZip(path string) (*zip.ReadCloser, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("not a valid .zip archive: %w", err)
	}
	var dbEntry *zip.File
	for _, f := range zr.File {
		if f.Name == "data.db" {
			dbEntry = f
			break
		}
	}
	if dbEntry == nil {
		zr.Close()
		return nil, fmt.Errorf("backup is missing data.db")
	}
	rc, err := dbEntry.Open()
	if err != nil {
		zr.Close()
		return nil, fmt.Errorf("data.db in backup is unreadable: %w", err)
	}
	// SQLite's fixed 16-byte magic header — cheap, doesn't require
	// extracting/opening the whole database just to prove it's at least
	// shaped like one before going further.
	header := make([]byte, 16)
	_, err = io.ReadFull(rc, header)
	rc.Close()
	if err != nil || string(header) != "SQLite format 3\x00" {
		zr.Close()
		return nil, fmt.Errorf("data.db in backup is not a valid SQLite database")
	}
	return zr, nil
}

// previewRestoreFromZip extracts just data.db from a validated backup zip
// to a temp file, ATTACHes it read-only alongside the live database, and
// reports row-count diffs per table — never writing anything.
func previewRestoreFromZip(ctx context.Context, sqlDB *sql.DB, zipPath string) (*restorePreview, error) {
	zr, err := validateBackupZip(zipPath)
	if err != nil {
		return nil, err
	}
	defer zr.Close()

	var dbEntry *zip.File
	fileCount := 0
	for _, f := range zr.File {
		switch {
		case f.Name == "data.db":
			dbEntry = f
		case strings.HasPrefix(f.Name, "files/") && !f.FileInfo().IsDir():
			fileCount++
		}
	}

	tmpDB, err := os.CreateTemp("", "onebox-preview-*.db")
	if err != nil {
		return nil, fmt.Errorf("stage backup for preview: %w", err)
	}
	tmpDBPath := tmpDB.Name()
	defer os.Remove(tmpDBPath)
	rc, err := dbEntry.Open()
	if err != nil {
		tmpDB.Close()
		return nil, fmt.Errorf("read backup database: %w", err)
	}
	_, copyErr := io.Copy(tmpDB, rc)
	rc.Close()
	tmpDB.Close()
	if copyErr != nil {
		return nil, fmt.Errorf("extract backup database: %w", copyErr)
	}

	if _, err := sqlDB.ExecContext(ctx, `ATTACH DATABASE ? AS bak`, tmpDBPath); err != nil {
		return nil, fmt.Errorf("attach backup for preview: %w", err)
	}
	defer sqlDB.ExecContext(ctx, `DETACH DATABASE bak`)

	backupTables, err := tableNames(ctx, sqlDB, "bak")
	if err != nil {
		return nil, err
	}
	liveTables, err := tableNames(ctx, sqlDB, "main")
	if err != nil {
		return nil, err
	}
	liveSet := make(map[string]bool, len(liveTables))
	for _, t := range liveTables {
		liveSet[t] = true
	}

	preview := &restorePreview{FilesInBackup: fileCount}
	for _, t := range backupTables {
		// Never actually restored — see restoreFromBackupDB's identical
		// _backups exclusion; the preview must match what a real restore
		// would do, or it isn't a preview.
		if t == "_backups" {
			continue
		}
		if !liveSet[t] {
			preview.TablesSkipped = append(preview.TablesSkipped, t)
			continue
		}
		var liveCount, backupCount int
		if err := sqlDB.QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM main.%q`, t)).Scan(&liveCount); err != nil {
			return nil, fmt.Errorf("count live rows in %s: %w", t, err)
		}
		if err := sqlDB.QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM bak.%q`, t)).Scan(&backupCount); err != nil {
			return nil, fmt.Errorf("count backup rows in %s: %w", t, err)
		}
		preview.TablesToRestore = append(preview.TablesToRestore, restoreTablePreview{Table: t, LiveRowCount: liveCount, BackupRowCount: backupCount})
	}
	return preview, nil
}

// restoreFromZip is the shared core of both restore entry points —
// handleImportBackup (a fresh upload, e.g. moving to a new instance) and
// handleRestoreFromBackup (an already-stored backup, selected from
// history) both just need to get an open *zip.ReadCloser in front of this
// one function, rather than each re-implementing "extract data.db,
// restore it, extract files/" independently.
func restoreFromZip(ctx context.Context, sqlDB *sql.DB, cfg config.Config, zr *zip.ReadCloser) (*restoreSummary, error) {
	var dbEntry *zip.File
	var fileEntries []*zip.File
	for _, f := range zr.File {
		if f.Name == "data.db" {
			dbEntry = f
		} else if strings.HasPrefix(f.Name, "files/") && !f.FileInfo().IsDir() {
			fileEntries = append(fileEntries, f)
		}
	}
	if dbEntry == nil {
		return nil, fmt.Errorf("backup is missing data.db")
	}

	tmpDB, err := os.CreateTemp("", "onebox-restore-*.db")
	if err != nil {
		return nil, fmt.Errorf("stage backup database: %w", err)
	}
	tmpDBPath := tmpDB.Name()
	defer os.Remove(tmpDBPath)
	rc, err := dbEntry.Open()
	if err != nil {
		tmpDB.Close()
		return nil, fmt.Errorf("read backup database: %w", err)
	}
	_, copyErr := io.Copy(tmpDB, rc)
	rc.Close()
	tmpDB.Close()
	if copyErr != nil {
		return nil, fmt.Errorf("extract backup database: %w", copyErr)
	}

	summary, err := restoreFromBackupDB(ctx, sqlDB, tmpDBPath)
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(cfg.FilesDir, 0o755); err == nil {
		for _, f := range fileEntries {
			name := strings.TrimPrefix(f.Name, "files/")
			if name == "" || strings.Contains(name, "..") {
				continue
			}
			if restoreOneFile(f, filepath.Join(cfg.FilesDir, name)) {
				summary.FilesRestored++
			}
		}
	}
	return summary, nil
}

// StartBackupScheduler runs a backup on cfg.BackupIntervalHours forever,
// until ctx is cancelled — a no-op (returns immediately) when scheduled
// backups aren't configured. Deliberately does not run one immediately at
// startup — the first scheduled backup fires after one full interval, so
// restarting the process repeatedly (e.g. during development) doesn't
// spam the backups directory.
func (s *Server) StartBackupScheduler(ctx context.Context) {
	if s.cfg.BackupIntervalHours <= 0 {
		return
	}
	interval := time.Duration(s.cfg.BackupIntervalHours) * time.Hour
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				meta, err := createBackupSnapshot(ctx, s, "scheduled")
				if err != nil {
					log.Printf("scheduled backup failed: %v", err)
					continue
				}
				log.Printf("scheduled backup %s created (%d table(s), %d bytes)", meta.ID, meta.TablesCount, meta.SizeBytes)
				if err := enforceBackupRetention(ctx, s.db, s.cfg, s.cfg.BackupRetentionCount); err != nil {
					log.Printf("backup retention: %v", err)
				}
			}
		}
	}()
}
