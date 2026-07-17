-- Week 2 will populate _users and _admins. This migration establishes the
-- _collections registry that every dynamic user collection is defined in.
CREATE TABLE _collections (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL UNIQUE,
    schema_json TEXT NOT NULL,
    rules_json  TEXT NOT NULL DEFAULT '{}',
    created    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
