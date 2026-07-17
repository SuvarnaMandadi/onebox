package db

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Migrate applies every embedded migration in migrations/ that hasn't
// already been recorded in the _migrations table, in filename order. Each
// migration runs inside its own transaction.
func Migrate(sqlDB *sql.DB) error {
	if _, err := sqlDB.Exec(`
		CREATE TABLE IF NOT EXISTS _migrations (
			name    TEXT PRIMARY KEY,
			applied TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
		)`); err != nil {
		return fmt.Errorf("create _migrations table: %w", err)
	}

	names, err := sortedMigrationNames()
	if err != nil {
		return err
	}

	for _, name := range names {
		applied, err := isApplied(sqlDB, name)
		if err != nil {
			return err
		}
		if applied {
			continue
		}
		if err := applyMigration(sqlDB, name); err != nil {
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
	}
	return nil
}

func sortedMigrationNames() ([]string, error) {
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read migrations dir: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

func isApplied(sqlDB *sql.DB, name string) (bool, error) {
	var count int
	err := sqlDB.QueryRow(`SELECT COUNT(*) FROM _migrations WHERE name = ?`, name).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("check migration %s: %w", name, err)
	}
	return count > 0, nil
}

func applyMigration(sqlDB *sql.DB, name string) error {
	contents, err := migrationFiles.ReadFile("migrations/" + name)
	if err != nil {
		return fmt.Errorf("read %s: %w", name, err)
	}

	tx, err := sqlDB.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(string(contents)); err != nil {
		return fmt.Errorf("exec sql: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO _migrations (name) VALUES (?)`, name); err != nil {
		return fmt.Errorf("record migration: %w", err)
	}
	return tx.Commit()
}
