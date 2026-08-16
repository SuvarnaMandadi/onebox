-- Backup history (RC3): previously a backup only ever existed as a
-- one-off streamed download (POST /api/backups/export), never persisted
-- anywhere — no history, no scheduling, no "download this again later."
-- This table is metadata only; the actual .zip lives on disk under
-- DataDir/backups/<id>.zip (see backups.go) — same split as _files/
-- FilesDir already uses for uploaded file content.
CREATE TABLE _backups (
    id           TEXT PRIMARY KEY,
    filename     TEXT NOT NULL,
    size_bytes   INTEGER NOT NULL DEFAULT 0,
    trigger      TEXT NOT NULL, -- "manual" or "scheduled"
    status       TEXT NOT NULL, -- "complete" or "failed"
    tables_count INTEGER NOT NULL DEFAULT 0,
    error        TEXT NOT NULL DEFAULT '',
    created      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX idx_backups_created ON _backups (created);
