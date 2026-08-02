package db

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
)

// TestOpenAppliesPragmasToEveryPooledConnection is the regression test
// for a real bug: pragmas set via a one-time sqlDB.Exec() right after
// sql.Open only ever land on whichever single connection serviced that
// call — database/sql's pool can open more physical connections later,
// on demand, under concurrent load, and each of those started with
// SQLite's real defaults (busy_timeout=0 in particular) instead of the
// 5s this package intends. That gap was invisible in single-threaded
// tests and surfaced live as concurrent chat-attachment uploads (a core
// "attach multiple files" scenario, which fires one INSERT per file
// concurrently) failing with "database is locked" on whichever upload
// didn't win the race. See pragmaDSNParams' doc comment for the fix
// (encoding pragmas in the DSN itself, which the driver applies to every
// connection it opens) and TestConcurrentWritesDoNotFailWithDatabaseLocked
// for the end-to-end proof.
func TestOpenAppliesPragmasToEveryPooledConnection(t *testing.T) {
	sqlDB, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer sqlDB.Close()

	// Force the pool to actually open several distinct physical
	// connections rather than reusing one, so this test can tell the
	// difference between "the first connection happened to get the
	// pragma" and "every connection gets it."
	sqlDB.SetMaxIdleConns(0)

	seen := map[int64]bool{}
	for i := 0; i < 5; i++ {
		conn, err := sqlDB.Conn(context.Background())
		if err != nil {
			t.Fatalf("Conn(): %v", err)
		}
		var timeout int64
		if err := conn.QueryRowContext(context.Background(), "PRAGMA busy_timeout").Scan(&timeout); err != nil {
			conn.Close()
			t.Fatalf("query busy_timeout: %v", err)
		}
		conn.Close() // returns to the pool as idle; SetMaxIdleConns(0) drops it, forcing a fresh connection next time
		if timeout != 5000 {
			t.Fatalf("connection %d: busy_timeout = %d, want 5000 (pragma not applied to this pooled connection)", i, timeout)
		}
		seen[timeout] = true
	}
}

// TestConcurrentWritesDoNotFailWithDatabaseLocked is the end-to-end
// reproduction of the live bug: many goroutines writing at once (the
// same shape as concurrent chat-attachment uploads, each doing its own
// INSERT into _files/_chat_attachments) must all succeed — the 5s
// busy_timeout should serialize them, not surface as an error, on every
// connection the pool hands out.
func TestConcurrentWritesDoNotFailWithDatabaseLocked(t *testing.T) {
	sqlDB, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer sqlDB.Close()

	if _, err := sqlDB.Exec(`CREATE TABLE scratch (id INTEGER PRIMARY KEY, val TEXT)`); err != nil {
		t.Fatalf("create scratch table: %v", err)
	}

	const n = 20
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = sqlDB.Exec(`INSERT INTO scratch (val) VALUES (?)`, "row")
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent write %d failed: %v", i, err)
		}
	}

	var count int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM scratch`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != n {
		t.Fatalf("count = %d, want %d", count, n)
	}
}

// TestMigrateThenOpenRoundTrip is a light sanity check that Open + Migrate
// still produces a usable, queryable database — nothing about the DSN
// change should affect schema application.
func TestMigrateThenOpenRoundTrip(t *testing.T) {
	sqlDB, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer sqlDB.Close()

	if err := Migrate(sqlDB); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	var name string
	if err := sqlDB.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = '_collections'`).Scan(&name); err != nil {
		t.Fatalf("expected the _collections table to exist after migration: %v", err)
	}
}
