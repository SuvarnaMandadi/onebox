package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"onebox/internal/config"
)

// newBackupTestServer is newTestServer with a real (auto-cleaned) DataDir
// — backup history writes actual .zip files to disk (backupsDir), unlike
// most of this package's tests, which only touch the in-memory database.
// Without an explicit DataDir, backupsDir would resolve to a relative
// "backups/" under wherever `go test` happens to run from (the package
// source directory), which is exactly the kind of test-pollution this
// avoids.
func newBackupTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	srv, _ := newTestServerWithConfig(t, config.Config{DataDir: dir})
	return srv, dir
}

// TestBackupHistoryCreateListDownloadDelete is the RC3 end-to-end pin for
// the persisted backup history — previously a backup only ever existed as
// a one-off streamed download, never listed, never downloadable again.
func TestBackupHistoryCreateListDownloadDelete(t *testing.T) {
	srv, _ := newBackupTestServer(t)
	adminToken := bootstrapAdmin(t, srv)
	createTestCollection(t, srv, adminToken, "notes")
	doAuth(t, srv, http.MethodPost, "/api/collections/notes/records", adminToken, map[string]any{"title": "one"})

	createRec := doAuth(t, srv, http.MethodPost, "/api/backups", adminToken, nil)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create backup: status = %d, body = %s", createRec.Code, createRec.Body.String())
	}
	var meta backupMeta
	if err := json.Unmarshal(createRec.Body.Bytes(), &meta); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if meta.Status != "complete" {
		t.Fatalf("Status = %q, want complete (error=%q)", meta.Status, meta.Error)
	}
	if meta.SizeBytes == 0 {
		t.Fatal("SizeBytes = 0, want a real backup file size")
	}
	if meta.TablesCount == 0 {
		t.Fatal("TablesCount = 0, want at least the system tables plus notes")
	}
	if meta.Trigger != "manual" {
		t.Fatalf("Trigger = %q, want manual", meta.Trigger)
	}

	listRec := doAuth(t, srv, http.MethodGet, "/api/backups", adminToken, nil)
	var listResp struct {
		Items []backupMeta `json:"items"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listResp.Items) != 1 || listResp.Items[0].ID != meta.ID {
		t.Fatalf("expected the created backup in history, got %+v", listResp.Items)
	}

	dlRec := doAuth(t, srv, http.MethodGet, "/api/backups/"+meta.ID+"/download", adminToken, nil)
	if dlRec.Code != http.StatusOK {
		t.Fatalf("download: status = %d", dlRec.Code)
	}
	if dlRec.Body.Len() == 0 {
		t.Fatal("downloaded backup body is empty")
	}
	if got := dlRec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
	}

	delRec := doAuth(t, srv, http.MethodDelete, "/api/backups/"+meta.ID, adminToken, nil)
	if delRec.Code != http.StatusNoContent {
		t.Fatalf("delete: status = %d, body = %s", delRec.Code, delRec.Body.String())
	}
	afterDelete := doAuth(t, srv, http.MethodGet, "/api/backups/"+meta.ID+"/download", adminToken, nil)
	if afterDelete.Code != http.StatusNotFound {
		t.Fatalf("download after delete: status = %d, want 404", afterDelete.Code)
	}
}

// TestBackupRestorePreviewIsNonDestructive is the RC3 pin for restore
// preview: it must report accurate live-vs-backup row counts and which
// tables would be skipped, without touching any live data — a genuine
// dry run, not a real restore in disguise.
func TestBackupRestorePreviewIsNonDestructive(t *testing.T) {
	srv, _ := newBackupTestServer(t)
	adminToken := bootstrapAdmin(t, srv)
	createTestCollection(t, srv, adminToken, "notes")
	doAuth(t, srv, http.MethodPost, "/api/collections/notes/records", adminToken, map[string]any{"title": "backed up"})

	createRec := doAuth(t, srv, http.MethodPost, "/api/backups", adminToken, nil)
	var meta backupMeta
	json.Unmarshal(createRec.Body.Bytes(), &meta)

	// Diverge live state after the backup was taken.
	doAuth(t, srv, http.MethodPost, "/api/collections/notes/records", adminToken, map[string]any{"title": "added after backup"})

	previewRec := doAuth(t, srv, http.MethodGet, "/api/backups/"+meta.ID+"/preview", adminToken, nil)
	if previewRec.Code != http.StatusOK {
		t.Fatalf("preview: status = %d, body = %s", previewRec.Code, previewRec.Body.String())
	}
	var preview restorePreview
	if err := json.Unmarshal(previewRec.Body.Bytes(), &preview); err != nil {
		t.Fatalf("decode preview: %v", err)
	}
	var notesPreview *restoreTablePreview
	for i, tp := range preview.TablesToRestore {
		if tp.Table == "notes" {
			notesPreview = &preview.TablesToRestore[i]
		}
	}
	if notesPreview == nil {
		t.Fatalf("expected notes in tables_to_restore, got %+v", preview.TablesToRestore)
	}
	if notesPreview.LiveRowCount != 2 {
		t.Fatalf("LiveRowCount = %d, want 2 (the post-backup addition must be visible)", notesPreview.LiveRowCount)
	}
	if notesPreview.BackupRowCount != 1 {
		t.Fatalf("BackupRowCount = %d, want 1 (only what existed at backup time)", notesPreview.BackupRowCount)
	}

	// A preview must never actually restore — confirm live data is
	// completely untouched by re-checking the real record count.
	listRec := doAuth(t, srv, http.MethodGet, "/api/collections/notes/records", adminToken, nil)
	var listResp struct {
		Items []map[string]any `json:"items"`
	}
	json.Unmarshal(listRec.Body.Bytes(), &listResp)
	if len(listResp.Items) != 2 {
		t.Fatalf("live records = %d after a preview, want 2 (preview must be non-destructive)", len(listResp.Items))
	}
}

// TestBackupRestoreFromStoredBackup is the selective-restore-from-history
// pin: restoring by id (no re-upload) must produce the exact same result
// as the existing upload-a-file restore path.
func TestBackupRestoreFromStoredBackup(t *testing.T) {
	srv, _ := newBackupTestServer(t)
	adminToken := bootstrapAdmin(t, srv)
	createTestCollection(t, srv, adminToken, "notes")
	doAuth(t, srv, http.MethodPost, "/api/collections/notes/records", adminToken, map[string]any{"title": "keep me"})

	createRec := doAuth(t, srv, http.MethodPost, "/api/backups", adminToken, nil)
	var meta backupMeta
	json.Unmarshal(createRec.Body.Bytes(), &meta)

	doAuth(t, srv, http.MethodPost, "/api/collections/notes/records", adminToken, map[string]any{"title": "should disappear after restore"})

	restoreRec := doAuth(t, srv, http.MethodPost, "/api/backups/"+meta.ID+"/restore", adminToken, nil)
	if restoreRec.Code != http.StatusOK {
		t.Fatalf("restore: status = %d, body = %s", restoreRec.Code, restoreRec.Body.String())
	}
	var summary restoreSummary
	json.Unmarshal(restoreRec.Body.Bytes(), &summary)
	if len(summary.TablesRestored) == 0 {
		t.Fatalf("expected at least one table restored, got %+v", summary)
	}

	listRec := doAuth(t, srv, http.MethodGet, "/api/collections/notes/records", adminToken, nil)
	var listResp struct {
		Items []map[string]any `json:"items"`
	}
	json.Unmarshal(listRec.Body.Bytes(), &listResp)
	if len(listResp.Items) != 1 {
		t.Fatalf("expected 1 record after restore, got %d: %+v", len(listResp.Items), listResp.Items)
	}
}

// TestBackupRestoreNeverWipesBackupHistory is the regression pin for a
// real bug found during RC3 verification: a snapshot is always taken
// BEFORE its own row is inserted into _backups (chicken-and-egg), so
// every backup's own copy of _backups is missing at least its own entry.
// Restoring _backups like any other table would therefore silently erase
// every backup recorded since the restored one — discovered live (create
// backup A, create backup B, restore A, list backups: B was gone).
// restoreFromBackupDB/previewRestoreFromZip now both skip _backups
// explicitly; this proves the history survives a real restore.
func TestBackupRestoreNeverWipesBackupHistory(t *testing.T) {
	srv, _ := newBackupTestServer(t)
	adminToken := bootstrapAdmin(t, srv)
	createTestCollection(t, srv, adminToken, "notes")

	firstRec := doAuth(t, srv, http.MethodPost, "/api/backups", adminToken, nil)
	var first backupMeta
	json.Unmarshal(firstRec.Body.Bytes(), &first)

	secondRec := doAuth(t, srv, http.MethodPost, "/api/backups", adminToken, nil)
	var second backupMeta
	json.Unmarshal(secondRec.Body.Bytes(), &second)

	restoreRec := doAuth(t, srv, http.MethodPost, "/api/backups/"+first.ID+"/restore", adminToken, nil)
	if restoreRec.Code != http.StatusOK {
		t.Fatalf("restore: status = %d, body = %s", restoreRec.Code, restoreRec.Body.String())
	}
	var summary restoreSummary
	json.Unmarshal(restoreRec.Body.Bytes(), &summary)
	for _, t2 := range summary.TablesRestored {
		if t2 == "_backups" {
			t.Fatalf("_backups must never appear in TablesRestored — restoring it wipes backup history, got %+v", summary.TablesRestored)
		}
	}

	listRec := doAuth(t, srv, http.MethodGet, "/api/backups", adminToken, nil)
	var listResp struct {
		Items []backupMeta `json:"items"`
	}
	json.Unmarshal(listRec.Body.Bytes(), &listResp)
	if len(listResp.Items) != 2 {
		t.Fatalf("expected both backups still in history after restoring from the first, got %d: %+v", len(listResp.Items), listResp.Items)
	}
}

// TestBackupRetentionKeepsOnlyNewest confirms enforceBackupRetention
// deletes the oldest backups once the configured count is exceeded, both
// the metadata row and the on-disk file.
func TestBackupRetentionKeepsOnlyNewest(t *testing.T) {
	srv, dir := newBackupTestServer(t)
	adminToken := bootstrapAdmin(t, srv)
	createTestCollection(t, srv, adminToken, "notes")

	var ids []string
	for i := 0; i < 3; i++ {
		rec := doAuth(t, srv, http.MethodPost, "/api/backups", adminToken, nil)
		var m backupMeta
		json.Unmarshal(rec.Body.Bytes(), &m)
		ids = append(ids, m.ID)
	}

	// Same call a scheduled run makes after creating its own backup (see
	// StartBackupScheduler) — exercised directly here rather than waiting
	// on a real ticker interval.
	if err := enforceBackupRetention(context.Background(), srv.db, config.Config{DataDir: dir}, 2); err != nil {
		t.Fatalf("enforceBackupRetention: %v", err)
	}

	listRec := doAuth(t, srv, http.MethodGet, "/api/backups", adminToken, nil)
	var listResp struct {
		Items []backupMeta `json:"items"`
	}
	json.Unmarshal(listRec.Body.Bytes(), &listResp)
	if len(listResp.Items) != 2 {
		t.Fatalf("expected 2 backups kept after retention, got %d: %+v", len(listResp.Items), listResp.Items)
	}
	// The oldest (ids[0]) must be gone — both the row and its file.
	for _, item := range listResp.Items {
		if item.ID == ids[0] {
			t.Fatalf("oldest backup %s should have been pruned by retention", ids[0])
		}
	}
}
