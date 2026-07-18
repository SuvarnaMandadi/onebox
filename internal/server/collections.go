package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// collection is a row from the _collections registry, with schema/rules
// decoded from their JSON columns.
type collection struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Schema      Schema `json:"schema"`
	Rules       Rules  `json:"rules"`
	RecordCount int    `json:"record_count"`
	Created     string `json:"created"`
	Updated     string `json:"updated"`
}

// countCollectionRecords reports how many rows are in a collection's
// dynamic table — used by the admin dashboard's Home page "total records"
// stat. A plain COUNT(*) is fine at v0.2 scale, consistent with the
// brute-force-is-fine-for-now approach already used for RAG similarity.
func countCollectionRecords(ctx context.Context, sqlDB *sql.DB, name string) (int, error) {
	var n int
	err := sqlDB.QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM %q`, name)).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count records in %s: %w", name, err)
	}
	return n, nil
}

var (
	errCollectionExists   = errors.New("collection already exists")
	errCollectionNotFound = errors.New("collection not found")
)

// createCollection validates name/schema/rules, then atomically creates the
// dynamic table and registers it in _collections.
func createCollection(ctx context.Context, sqlDB *sql.DB, name string, schema Schema, rules Rules) (*collection, error) {
	if err := ValidateCollectionName(name); err != nil {
		return nil, err
	}
	if err := ValidateSchema(schema); err != nil {
		return nil, err
	}
	if err := ValidateRules(rules); err != nil {
		return nil, err
	}
	rules = fillDefaultRules(rules)

	schemaJSON, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("marshal schema: %w", err)
	}
	rulesJSON, err := json.Marshal(rules)
	if err != nil {
		return nil, fmt.Errorf("marshal rules: %w", err)
	}

	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, createTableSQL(name, schema)); err != nil {
		if isTableExistsErr(err) {
			return nil, errCollectionExists
		}
		return nil, fmt.Errorf("create table: %w", err)
	}

	id := uuid.NewString()
	_, err = tx.ExecContext(ctx,
		`INSERT INTO _collections (id, name, schema_json, rules_json) VALUES (?, ?, ?, ?)`,
		id, name, string(schemaJSON), string(rulesJSON),
	)
	if err != nil {
		if isUniqueConstraintErr(err) {
			return nil, errCollectionExists
		}
		return nil, fmt.Errorf("insert collection: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}

	return getCollectionByName(ctx, sqlDB, name)
}

func fillDefaultRules(rules Rules) Rules {
	def := DefaultRules()
	if rules.List == "" {
		rules.List = def.List
	}
	if rules.View == "" {
		rules.View = def.View
	}
	if rules.Create == "" {
		rules.Create = def.Create
	}
	if rules.Update == "" {
		rules.Update = def.Update
	}
	if rules.Delete == "" {
		rules.Delete = def.Delete
	}
	return rules
}

func getCollectionByName(ctx context.Context, sqlDB *sql.DB, name string) (*collection, error) {
	row := sqlDB.QueryRowContext(ctx,
		`SELECT id, name, schema_json, rules_json, created, updated FROM _collections WHERE name = ?`,
		name,
	)
	c, err := scanCollection(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errCollectionNotFound
	}
	if err != nil {
		return nil, err
	}
	if c.RecordCount, err = countCollectionRecords(ctx, sqlDB, c.Name); err != nil {
		return nil, err
	}
	return c, nil
}

func listCollections(ctx context.Context, sqlDB *sql.DB) ([]*collection, error) {
	rows, err := sqlDB.QueryContext(ctx,
		`SELECT id, name, schema_json, rules_json, created, updated FROM _collections ORDER BY name`,
	)
	if err != nil {
		return nil, fmt.Errorf("query collections: %w", err)
	}
	defer rows.Close()

	var out []*collection
	for rows.Next() {
		c, err := scanCollectionRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for _, c := range out {
		if c.RecordCount, err = countCollectionRecords(ctx, sqlDB, c.Name); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// deleteCollection atomically drops the dynamic table and removes it from
// the registry.
func deleteCollection(ctx context.Context, sqlDB *sql.DB, name string) error {
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `DELETE FROM _collections WHERE name = ?`, name)
	if err != nil {
		return fmt.Errorf("delete collection row: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if affected == 0 {
		return errCollectionNotFound
	}

	if _, err := tx.ExecContext(ctx, fmt.Sprintf("DROP TABLE %q", name)); err != nil {
		return fmt.Errorf("drop table: %w", err)
	}

	return tx.Commit()
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanCollection(row rowScanner) (*collection, error) {
	return scanCollectionRow(row)
}

func scanCollectionRow(row rowScanner) (*collection, error) {
	var c collection
	var schemaJSON, rulesJSON string
	if err := row.Scan(&c.ID, &c.Name, &schemaJSON, &rulesJSON, &c.Created, &c.Updated); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(schemaJSON), &c.Schema); err != nil {
		return nil, fmt.Errorf("unmarshal schema: %w", err)
	}
	if err := json.Unmarshal([]byte(rulesJSON), &c.Rules); err != nil {
		return nil, fmt.Errorf("unmarshal rules: %w", err)
	}
	return &c, nil
}

func isTableExistsErr(err error) bool {
	return err != nil && strings.Contains(err.Error(), "already exists")
}
