-- _provider_diagnostics is Settings' Connection History (Section 7): one
-- row per diagnostics run, whether triggered manually ("Re-test") or by
-- opening the diagnostics panel. Deliberately small — provider, success,
-- a short summary, duration, timestamp — never the full report body
-- (models list, every check, every issue): that's already reproducible
-- on demand by re-running the diagnostic, and this table exists purely
-- to answer "when did this last work, and what changed," not to be a
-- long-term audit log.
CREATE TABLE _provider_diagnostics (
    id          TEXT PRIMARY KEY,
    provider    TEXT NOT NULL,
    success     INTEGER NOT NULL,
    summary     TEXT NOT NULL DEFAULT '',
    duration_ms INTEGER NOT NULL DEFAULT 0,
    created     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX idx_provider_diagnostics_provider_created ON _provider_diagnostics (provider, created DESC);
