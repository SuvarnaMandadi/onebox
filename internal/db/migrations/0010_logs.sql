-- Capped request log for the admin Logs page. Kept bounded by
-- internal/server/logs.go's insertLogEntry, which occasionally trims to
-- the newest N rows rather than growing forever.
CREATE TABLE _logs (
    id          TEXT PRIMARY KEY,
    time        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    method      TEXT NOT NULL,
    path        TEXT NOT NULL,
    status      INTEGER NOT NULL,
    user_id     TEXT,
    duration_ms INTEGER NOT NULL
);
CREATE INDEX idx_logs_time ON _logs (time DESC);
