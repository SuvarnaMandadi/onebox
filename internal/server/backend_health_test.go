package server

import (
	"context"
	"net/http"
	"testing"
)

// TestBuildBackendHealthReportRealChecks uses newBackupTestServer (a real
// t.TempDir() DataDir, not just an in-memory DB with no filesystem
// backing) so the disk-usage syscall and DB file stat have something
// real to check against — see diskspace_windows.go/diskspace_unix.go.
func TestBuildBackendHealthReportRealChecks(t *testing.T) {
	srv, _ := newBackupTestServer(t)
	adminToken := bootstrapAdmin(t, srv)
	createTestCollection(t, srv, adminToken, "notes")
	doAuth(t, srv, http.MethodPost, "/api/collections/notes/records", adminToken, map[string]any{"title": "one"})

	report, err := srv.buildBackendHealthReport(context.Background())
	if err != nil {
		t.Fatalf("buildBackendHealthReport() error = %v", err)
	}

	if !report.Database.Reachable {
		t.Error("Database.Reachable = false, want true — the test DB is a real, open connection")
	}
	if !report.Disk.Available {
		t.Errorf("Disk.Available = false (error: %s), want true — DataDir is a real temp directory", report.Disk.Error)
	}
	if report.Disk.TotalBytes == 0 {
		t.Error("Disk.TotalBytes = 0, want a real nonzero value from the OS")
	}
	if report.Counts.Collections != 1 {
		t.Errorf("Counts.Collections = %d, want 1", report.Counts.Collections)
	}
	if report.Counts.Records != 1 {
		t.Errorf("Counts.Records = %d, want 1", report.Counts.Records)
	}
	if report.Build.MigrationCount == 0 {
		t.Error("Build.MigrationCount = 0, want the real applied-migration count")
	}
	if report.Build.LatestMigration == "" {
		t.Error("Build.LatestMigration is empty, want the real latest migration filename")
	}
	// Process metrics reuse currentProcessMetrics (metrics.go) — just
	// confirm it's actually populated, not re-testing that function's own
	// logic (already covered by metrics_test.go if it exists).
	if report.Process.Goroutines == 0 {
		t.Error("Process.Goroutines = 0 — runtime.NumGoroutine() should never report zero for a running process")
	}
}

func TestBuildBackendHealthReportBackupSchedulerStatus(t *testing.T) {
	srv, _ := newBackupTestServer(t)
	adminToken := bootstrapAdmin(t, srv)

	report, err := srv.buildBackendHealthReport(context.Background())
	if err != nil {
		t.Fatalf("buildBackendHealthReport() error = %v", err)
	}
	if report.BackupScheduler.Enabled {
		t.Error("BackupScheduler.Enabled = true, want false — newBackupTestServer sets no BackupIntervalHours")
	}
	if report.BackupScheduler.BackupCount != 0 {
		t.Fatalf("BackupScheduler.BackupCount = %d, want 0 before any backup is created", report.BackupScheduler.BackupCount)
	}

	createRec := doAuth(t, srv, http.MethodPost, "/api/backups", adminToken, nil)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create backup: status = %d, body = %s", createRec.Code, createRec.Body.String())
	}

	report2, err := srv.buildBackendHealthReport(context.Background())
	if err != nil {
		t.Fatalf("buildBackendHealthReport() (after backup) error = %v", err)
	}
	if report2.BackupScheduler.BackupCount != 1 {
		t.Fatalf("BackupScheduler.BackupCount = %d, want 1 after creating a backup", report2.BackupScheduler.BackupCount)
	}
	if report2.Counts.Backups != 1 {
		t.Fatalf("Counts.Backups = %d, want 1", report2.Counts.Backups)
	}
	if report2.BackupScheduler.LastBackupStatus != "complete" {
		t.Errorf("LastBackupStatus = %q, want complete", report2.BackupScheduler.LastBackupStatus)
	}
}

func TestHandleBackendHealthRequiresAdminAuth(t *testing.T) {
	srv, _ := newTestServer(t)
	rec := doAuth(t, srv, http.MethodGet, "/api/settings/backend-health", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 without a token", rec.Code)
	}
}

func TestHandleBackendHealthEndToEnd(t *testing.T) {
	srv, _ := newBackupTestServer(t)
	adminToken := bootstrapAdmin(t, srv)

	rec := doAuth(t, srv, http.MethodGet, "/api/settings/backend-health", adminToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
}
