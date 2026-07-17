CREATE TABLE _rag_sources (
    id          TEXT PRIMARY KEY,
    owner_id    TEXT,
    file_id     TEXT NOT NULL,
    filename    TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'pending', -- pending, processing, done, error
    chunk_count INTEGER NOT NULL DEFAULT 0,
    error       TEXT,
    created     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX idx_rag_sources_owner ON _rag_sources (owner_id);

CREATE TABLE _rag_chunks (
    id        TEXT PRIMARY KEY,
    source_id TEXT NOT NULL REFERENCES _rag_sources (id) ON DELETE CASCADE,
    owner_id  TEXT,
    position  INTEGER NOT NULL,
    text      TEXT NOT NULL,
    embedding BLOB NOT NULL,
    created   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX idx_rag_chunks_source ON _rag_chunks (source_id);
CREATE INDEX idx_rag_chunks_owner ON _rag_chunks (owner_id);
