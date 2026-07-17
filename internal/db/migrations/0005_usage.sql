CREATE TABLE _usage (
    id            TEXT PRIMARY KEY,
    user_id       TEXT,
    provider      TEXT NOT NULL,
    model         TEXT NOT NULL,
    tokens_in     INTEGER NOT NULL DEFAULT 0,
    tokens_out    INTEGER NOT NULL DEFAULT 0,
    cost_estimate REAL NOT NULL DEFAULT 0,
    cached        INTEGER NOT NULL DEFAULT 0,
    created       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX idx_usage_user_id ON _usage (user_id);
CREATE INDEX idx_usage_created ON _usage (created);
