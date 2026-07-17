CREATE TABLE _files (
    id       TEXT PRIMARY KEY,
    owner_id TEXT,
    filename TEXT NOT NULL,
    path     TEXT NOT NULL,
    size     INTEGER NOT NULL,
    mime     TEXT NOT NULL,
    created  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX idx_files_owner_id ON _files (owner_id);
