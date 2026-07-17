// Package db owns the SQLite connection and schema migrations for onebox.
package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

// Open opens the SQLite database at path in WAL mode with sane concurrency
// pragmas, creating its parent directory if needed. The pure-Go
// modernc.org/sqlite driver is used (no cgo) so the server keeps
// cross-compiling to Windows/Mac/Linux from a single machine.
//
// path == ":memory:" is treated specially, for tests: database/sql's
// connection pool can open more than one physical connection, and a bare
// ":memory:" DSN gives each connection its own separate, empty database —
// any concurrent access (e.g. a background goroutine racing a request
// handler) silently sees a different, tableless database. A uniquely
// named shared-cache URI keeps every connection opened by this *sql.DB
// pointed at the same in-memory database, while still giving each Open
// call its own isolated database from every other call.
func Open(path string) (*sql.DB, error) {
	dsn := path
	if path == ":memory:" {
		dsn = "file:" + uuid.NewString() + "?mode=memory&cache=shared"
	} else if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create data dir: %w", err)
		}
	}

	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	// SQLite only allows one writer at a time; WAL mode lets readers
	// proceed concurrently with a writer instead of blocking.
	pragmas := []string{
		"PRAGMA journal_mode = WAL;",
		"PRAGMA busy_timeout = 5000;",
		"PRAGMA foreign_keys = ON;",
		"PRAGMA synchronous = NORMAL;",
	}
	for _, p := range pragmas {
		if _, err := sqlDB.Exec(p); err != nil {
			sqlDB.Close()
			return nil, fmt.Errorf("set pragma %q: %w", p, err)
		}
	}

	return sqlDB, nil
}
