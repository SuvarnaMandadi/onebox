package server

import (
	"archive/zip"
	"context"
	"database/sql"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/go-chi/chi/v5"
)

// handleExportBackup is admin-only: streams a .zip containing a
// consistent snapshot of the SQLite database (via VACUUM INTO, so the
// live connection is never paused or locked out) plus every stored file.
func (s *Server) handleExportBackup(w http.ResponseWriter, r *http.Request) {
	tmp, err := os.CreateTemp("", "onebox-backup-*.db")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to prepare backup", nil)
		return
	}
	tmpPath := tmp.Name()
	tmp.Close()
	os.Remove(tmpPath) // VACUUM INTO requires the target not exist yet
	defer os.Remove(tmpPath)

	if _, err := s.db.ExecContext(r.Context(), fmt.Sprintf("VACUUM INTO %q", tmpPath)); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to snapshot database: "+err.Error(), nil)
		return
	}

	filename := "onebox-backup-" + time.Now().UTC().Format("2006-01-02-150405") + ".zip"
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)

	zw := zip.NewWriter(w)
	defer zw.Close()

	if err := addFileToZip(zw, tmpPath, "data.db"); err != nil {
		log.Printf("backup export: %v", err)
		return
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
}

func addFileToZip(zw *zip.Writer, srcPath, zipPath string) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := zw.Create(zipPath)
	if err != nil {
		return err
	}
	_, err = io.Copy(dst, src)
	return err
}

type restoreSummary struct {
	TablesRestored []string `json:"tables_restored"`
	TablesSkipped  []string `json:"tables_skipped"`
	FilesRestored  int      `json:"files_restored"`
}

// handleImportBackup is admin-only: restores from a .zip produced by
// handleExportBackup. Table data is merged into the *live* database via
// ATTACH + DELETE/INSERT per table (no server restart or connection swap
// needed); any table present in the backup but not in this instance's
// current schema is skipped and reported rather than guessed at.
func (s *Server) handleImportBackup(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<30) // 1 GiB ceiling for a backup upload
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_upload", "file too large or not a valid multipart/form-data upload", nil)
		return
	}
	defer r.MultipartForm.RemoveAll()

	file, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing_file", `expected a "file" multipart field`, nil)
		return
	}
	defer file.Close()

	tmpZip, err := os.CreateTemp("", "onebox-restore-*.zip")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to stage upload", nil)
		return
	}
	defer os.Remove(tmpZip.Name())
	if _, err := io.Copy(tmpZip, file); err != nil {
		tmpZip.Close()
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to read upload", nil)
		return
	}
	tmpZip.Close()

	zr, err := zip.OpenReader(tmpZip.Name())
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_backup", "not a valid backup .zip", nil)
		return
	}
	defer zr.Close()

	summary, err := restoreFromZip(r.Context(), s.db, s.cfg, zr)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "restore failed: "+err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

func restoreOneFile(f *zip.File, destPath string) bool {
	src, err := f.Open()
	if err != nil {
		return false
	}
	defer src.Close()
	dst, err := os.Create(destPath)
	if err != nil {
		return false
	}
	defer dst.Close()
	_, err = io.Copy(dst, src)
	return err == nil
}

// restoreFromBackupDB attaches backupPath and, for every table that
// exists in *both* the backup and the live schema, replaces the live
// rows with the backup's. Tables only present in the backup (e.g. a
// collection that's since been deleted here) are reported, not restored.
func restoreFromBackupDB(ctx context.Context, sqlDB *sql.DB, backupPath string) (*restoreSummary, error) {
	if _, err := sqlDB.ExecContext(ctx, `ATTACH DATABASE ? AS bak`, backupPath); err != nil {
		return nil, fmt.Errorf("attach backup: %w", err)
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

	summary := &restoreSummary{}
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	for _, t := range backupTables {
		// _backups (RC3) is infrastructure metadata about backup history
		// itself, not product data — restoring it would be actively wrong:
		// a snapshot is always taken before its own row is inserted (see
		// createBackupSnapshot), so every backup's _backups table is
		// missing at least its own row, and restoring it would silently
		// erase every backup recorded since. Backup history is left alone
		// by every restore, the same way it isn't touched by an admin
		// restoring their product data.
		if t == "_backups" {
			continue
		}
		if !liveSet[t] {
			summary.TablesSkipped = append(summary.TablesSkipped, t)
			continue
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM main.%q`, t)); err != nil {
			return nil, fmt.Errorf("clear table %s: %w", t, err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`INSERT INTO main.%q SELECT * FROM bak.%q`, t, t)); err != nil {
			return nil, fmt.Errorf("restore table %s: %w", t, err)
		}
		summary.TablesRestored = append(summary.TablesRestored, t)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit restore: %w", err)
	}
	return summary, nil
}

func tableNames(ctx context.Context, sqlDB *sql.DB, schema string) ([]string, error) {
	rows, err := sqlDB.QueryContext(ctx, fmt.Sprintf(`SELECT name FROM %s.sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%%'`, schema))
	if err != nil {
		return nil, fmt.Errorf("list tables in %s: %w", schema, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// -- backup history (RC3) -----------------------------------------------
//
// handleExportBackup/handleImportBackup above are untouched — the classic
// dashboard's Backups page calls them directly for a one-shot download/
// upload-restore, and that keeps working exactly as before. Everything
// below is additive: a persisted history around the same snapshot/restore
// primitives (see backups.go).

// handleCreateBackup is admin-only: creates a backup, persists it under
// backupsDir, and returns its metadata — unlike handleExportBackup, this
// doesn't stream the file back; GET .../download does that separately, so
// "create a backup" and "download a backup" are independent actions (a
// scheduled backup obviously has no request to stream a response to).
func (s *Server) handleCreateBackup(w http.ResponseWriter, r *http.Request) {
	meta, err := createBackupSnapshot(r.Context(), s, "manual")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "backup failed: "+err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusCreated, meta)
}

func (s *Server) handleListBackups(w http.ResponseWriter, r *http.Request) {
	items, err := listBackupHistory(r.Context(), s.db)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to list backups", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleDownloadBackup(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	meta, err := getBackupMeta(r.Context(), s.db, id)
	if err == errBackupNotFound {
		writeError(w, http.StatusNotFound, "not_found", "backup not found", nil)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to load backup", nil)
		return
	}
	if meta.Status != "complete" {
		writeError(w, http.StatusConflict, "backup_not_complete", "this backup did not complete successfully and has no file to download", nil)
		return
	}
	path := filepath.Join(backupsDir(s.cfg), id+".zip")
	f, err := os.Open(path)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "backup file is missing from disk", nil)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+meta.Filename+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	io.Copy(w, f)
}

func (s *Server) handleDeleteBackup(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := deleteBackupByID(r.Context(), s.db, s.cfg, id); err == errBackupNotFound {
		writeError(w, http.StatusNotFound, "not_found", "backup not found", nil)
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to delete backup", nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handlePreviewRestore is RC3's restore-preview: reports what a restore
// from this stored backup would actually do (per-table live vs. backup
// row counts, which tables would be skipped, how many files) without
// touching any live data — so an admin can see the blast radius of a
// restore before confirming the destructive one below.
func (s *Server) handlePreviewRestore(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	meta, err := getBackupMeta(r.Context(), s.db, id)
	if err == errBackupNotFound {
		writeError(w, http.StatusNotFound, "not_found", "backup not found", nil)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to load backup", nil)
		return
	}
	if meta.Status != "complete" {
		writeError(w, http.StatusConflict, "backup_not_complete", "this backup did not complete successfully and cannot be previewed", nil)
		return
	}
	preview, err := previewRestoreFromZip(r.Context(), s.db, filepath.Join(backupsDir(s.cfg), id+".zip"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_backup", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

// handleRestoreFromBackup restores from an already-stored backup by id —
// the selective-restore counterpart to handleImportBackup (which requires
// a fresh upload every time, e.g. for moving to a new instance). Reuses
// the exact same restoreFromBackupDB + restoreOneFile primitives.
func (s *Server) handleRestoreFromBackup(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	meta, err := getBackupMeta(r.Context(), s.db, id)
	if err == errBackupNotFound {
		writeError(w, http.StatusNotFound, "not_found", "backup not found", nil)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to load backup", nil)
		return
	}
	if meta.Status != "complete" {
		writeError(w, http.StatusConflict, "backup_not_complete", "this backup did not complete successfully and cannot be restored", nil)
		return
	}
	zipPath := filepath.Join(backupsDir(s.cfg), id+".zip")
	zr, err := validateBackupZip(zipPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_backup", err.Error(), nil)
		return
	}
	defer zr.Close()

	summary, err := restoreFromZip(r.Context(), s.db, s.cfg, zr)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "restore failed: "+err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, summary)
}
