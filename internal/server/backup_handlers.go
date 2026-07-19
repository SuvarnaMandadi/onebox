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
	"strings"
	"time"
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
		writeError(w, http.StatusBadRequest, "invalid_backup", "backup is missing data.db", nil)
		return
	}

	tmpDB, err := os.CreateTemp("", "onebox-restore-*.db")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to stage backup database", nil)
		return
	}
	tmpDBPath := tmpDB.Name()
	defer os.Remove(tmpDBPath)
	rc, err := dbEntry.Open()
	if err != nil {
		tmpDB.Close()
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to read backup database", nil)
		return
	}
	_, copyErr := io.Copy(tmpDB, rc)
	rc.Close()
	tmpDB.Close()
	if copyErr != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to extract backup database", nil)
		return
	}

	summary, err := restoreFromBackupDB(r.Context(), s.db, tmpDBPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "restore failed: "+err.Error(), nil)
		return
	}

	if err := os.MkdirAll(s.cfg.FilesDir, 0o755); err == nil {
		for _, f := range fileEntries {
			name := strings.TrimPrefix(f.Name, "files/")
			if name == "" || strings.Contains(name, "..") {
				continue
			}
			if restoreOneFile(f, filepath.Join(s.cfg.FilesDir, name)) {
				summary.FilesRestored++
			}
		}
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
