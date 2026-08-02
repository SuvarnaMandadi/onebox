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

// pragmaDSNParams are appended to every DSN Open builds — modernc.org/sqlite
// applies each "_pragma=name(value)" query parameter to a connection as
// part of opening it (see applyQueryParams in that driver), which is what
// makes this reliable in a way a one-time sqlDB.Exec("PRAGMA ...") right
// after sql.Open is NOT: database/sql's connection pool can open more
// physical connections later, on demand, under concurrent load — a
// pragma set via Exec only ever touches whichever single connection
// serviced that particular call, so any later connection the pool opens
// starts with SQLite's real defaults (busy_timeout=0 in particular)
// instead. That gap is exactly what surfaced as a real bug: two
// attachments uploaded together (a core "multiple files" scenario) fire
// concurrent INSERTs, and whichever request landed on a connection that
// never got the pragmas applied failed immediately with "database is
// locked" instead of waiting the intended 5s. Encoding the pragmas in
// the DSN itself makes every connection the pool ever opens configured
// identically, no matter when it's opened.
const pragmaDSNParams = "_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)&_pragma=synchronous(NORMAL)"

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
	var dsn string
	if path == ":memory:" {
		dsn = "file:" + uuid.NewString() + "?mode=memory&cache=shared&" + pragmaDSNParams
	} else {
		if dir := filepath.Dir(path); dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, fmt.Errorf("create data dir: %w", err)
			}
		}
		dsn = path + "?" + pragmaDSNParams
	}

	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	return sqlDB, nil
}
