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
	// "Customer Details" -> "customer_details" (RC2) — a name a human
	// would naturally type is turned into a legal identifier here, once,
	// rather than requiring every caller (dashboard, AI tool call) to
	// pre-clean it themselves. A name that's already legal is untouched.
	name = SlugifyCollectionName(name)
	if err := ValidateCollectionName(name); err != nil {
		return nil, err
	}
	if err := ValidateSchema(schema); err != nil {
		return nil, err
	}
	if err := validateRelationTargets(ctx, sqlDB, schema); err != nil {
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

// updateCollectionSchema replaces a collection's field list, rebuilding the
// underlying table so renames/type-changes/reorders/removals all take
// effect immediately: SQLite can ADD/RENAME/DROP columns individually but
// has no ALTER COLUMN TYPE, so one full rebuild (new table, copy data with
// a best-effort CAST, swap) handles every kind of change uniformly instead
// of five different code paths. Data for a removed field is lost; data for
// a type change is carried over via SQLite's permissive CAST (e.g.
// non-numeric text becomes 0, not an error) — the caller is expected to
// warn about both before calling this. That warning belongs at the UI
// layer, not the API contract: an API client scripting a schema migration
// directly should not be blocked by a confirmation dialog.
func updateCollectionSchema(ctx context.Context, sqlDB *sql.DB, name string, newSchema Schema) (*collection, error) {
	if err := ValidateSchema(newSchema); err != nil {
		return nil, err
	}
	if err := validateRelationTargets(ctx, sqlDB, newSchema); err != nil {
		return nil, err
	}

	existing, err := getCollectionByName(ctx, sqlDB, name)
	if err != nil {
		return nil, err
	}
	oldByName := make(map[string]Field, len(existing.Schema.Fields))
	for _, f := range existing.Schema.Fields {
		oldByName[f.Name] = f
	}

	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	tmpName := name + "__rebuild_tmp"
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("ALTER TABLE %q RENAME TO %q", name, tmpName)); err != nil {
		return nil, fmt.Errorf("rename old table: %w", err)
	}

	// persisted drops RenameFrom (a request-only hint) before this schema
	// is written to _collections or used to build the new table.
	persisted := Schema{Fields: make([]Field, len(newSchema.Fields))}
	insertCols := []string{"id", "owner_id", "created", "updated"}
	selectExprs := []string{"id", "owner_id", "created", "updated"}

	for i, f := range newSchema.Fields {
		// RC4 bug fix: this used to copy only Name/Type/Required, silently
		// dropping RelationCollection and Validation from EVERY field on
		// EVERY schema update (not just the one being changed) — a relation
		// field survived exactly one add-field/edit-schema round before its
		// relation_collection (and any field's validation rules) vanished
		// from what actually got persisted, even though the request that
		// triggered the rebuild had submitted the full, correct field data
		// and validateRelationTargets above had validated against it. Only
		// RenameFrom is deliberately request-only and excluded here — see
		// this field's own doc comment (collection_schema.go).
		persisted.Fields[i] = Field{Name: f.Name, Type: f.Type, Required: f.Required, RelationCollection: f.RelationCollection, Validation: f.Validation}

		source := f.Name
		if f.RenameFrom != "" {
			source = f.RenameFrom
		}
		old, existed := oldByName[source]
		insertCols = append(insertCols, fmt.Sprintf("%q", f.Name))
		switch {
		case !existed:
			selectExprs = append(selectExprs, "NULL")
		case old.Type == f.Type:
			selectExprs = append(selectExprs, fmt.Sprintf("%q", source))
		default:
			selectExprs = append(selectExprs, fmt.Sprintf("CAST(%q AS %s)", source, f.Type.sqliteType()))
		}
	}

	if _, err := tx.ExecContext(ctx, createTableSQL(name, persisted)); err != nil {
		return nil, fmt.Errorf("create rebuilt table: %w", err)
	}

	copySQL := fmt.Sprintf("INSERT INTO %q (%s) SELECT %s FROM %q",
		name, strings.Join(insertCols, ", "), strings.Join(selectExprs, ", "), tmpName)
	if _, err := tx.ExecContext(ctx, copySQL); err != nil {
		return nil, fmt.Errorf("copy data into rebuilt table: %w", err)
	}

	if _, err := tx.ExecContext(ctx, fmt.Sprintf("DROP TABLE %q", tmpName)); err != nil {
		return nil, fmt.Errorf("drop old table: %w", err)
	}

	schemaJSON, err := json.Marshal(persisted)
	if err != nil {
		return nil, fmt.Errorf("marshal schema: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE _collections SET schema_json = ?, updated = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE name = ?`,
		string(schemaJSON), name,
	); err != nil {
		return nil, fmt.Errorf("update collection registry: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}

	return getCollectionByName(ctx, sqlDB, name)
}

// validateRelationTargets checks that every relation field in schema names
// a collection that actually exists — the data-layer half of relation
// validation (ValidateSchema, collection_schema.go, already checked the
// structural half: relation_collection is set and syntactically a legal
// name). Reuses getCollectionByName, the same lookup every other
// existence check in this file already goes through — no parallel
// "does this collection exist" query. Called by both createCollection and
// updateCollectionSchema before either touches the database, so a schema
// naming a nonexistent (or misspelled) target collection is rejected up
// front rather than silently accepted and only failing later at record
// time (see validateRelationValues, records.go, for that record-level
// counterpart).
//
// A collection referencing itself (a self-relation, e.g. an "employees"
// collection with a manager_id field pointing back at "employees") is
// legal here by construction: updateCollectionSchema's existing lookup
// finds the collection because it already exists by the time a schema
// update runs. createCollection has no such case to handle specially
// either — a brand-new collection can't self-reference on its very first
// schema (the collection doesn't exist yet to be found), so that request
// is correctly rejected the same way any other nonexistent target would
// be; a self-relation can always be added afterwards via update_schema.
func validateRelationTargets(ctx context.Context, sqlDB *sql.DB, schema Schema) error {
	for _, f := range schema.Fields {
		if f.Type != FieldRelation {
			continue
		}
		if _, err := getCollectionByName(ctx, sqlDB, f.RelationCollection); err != nil {
			if err == errCollectionNotFound {
				return fmt.Errorf("field %q relates to collection %q, which does not exist", f.Name, f.RelationCollection)
			}
			return fmt.Errorf("checking relation target %q for field %q: %w", f.RelationCollection, f.Name, err)
		}
	}
	return nil
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
