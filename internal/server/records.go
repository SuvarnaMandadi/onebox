package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// recordListParams are the parsed, validated query params for a records
// list request.
type recordListParams struct {
	filters    map[string]string // field -> exact-match value, ANDed
	descending bool              // sort=-created (default) vs sort=created
	limit      int
	cursorTime string
	cursorID   string
}

const (
	defaultLimit = 30
	maxLimit     = 200
)

// scanRecords converts *sql.Rows from a dynamic collection table into
// JSON-friendly maps, decoding json-typed columns and converting
// SQLite's 0/1 integers back into real bools per the schema.
func scanRecords(rows *sql.Rows, schema Schema) ([]map[string]any, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("columns: %w", err)
	}

	fieldTypes := make(map[string]FieldType, len(schema.Fields))
	for _, f := range schema.Fields {
		fieldTypes[f.Name] = f.Type
	}

	var out []map[string]any
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}

		rec := make(map[string]any, len(cols))
		for i, col := range cols {
			rec[col] = convertColumnValue(fieldTypes[col], vals[i])
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

func convertColumnValue(ft FieldType, v any) any {
	if v == nil {
		return nil
	}
	switch ft {
	case FieldBool:
		switch n := v.(type) {
		case int64:
			return n != 0
		}
	case FieldJSON:
		if s, ok := v.(string); ok {
			var decoded any
			if err := json.Unmarshal([]byte(s), &decoded); err == nil {
				return decoded
			}
		}
	}
	return v
}

// validateRecordInput checks input against schema: required fields must be
// present (for create), and any provided field must have a value matching
// its declared type. Unknown fields (not declared in schema and not
// system columns) are rejected.
func validateRecordInput(input map[string]any, schema Schema, forCreate bool) error {
	declared := make(map[string]Field, len(schema.Fields))
	for _, f := range schema.Fields {
		declared[f.Name] = f
	}

	for name := range input {
		if systemColumns[name] {
			return fmt.Errorf("field %q is managed by the server and cannot be set", name)
		}
		if _, ok := declared[name]; !ok {
			return fmt.Errorf("unknown field %q", name)
		}
	}

	for _, f := range schema.Fields {
		v, present := input[f.Name]
		if !present {
			if forCreate && f.Required {
				return fmt.Errorf("field %q is required", f.Name)
			}
			continue
		}
		if v == nil {
			if f.Required {
				return fmt.Errorf("field %q is required", f.Name)
			}
			continue
		}
		if err := checkFieldType(f, v); err != nil {
			return err
		}
	}
	return nil
}

func checkFieldType(f Field, v any) error {
	switch f.Type {
	case FieldText, FieldDate:
		if _, ok := v.(string); !ok {
			return fmt.Errorf("field %q must be a string", f.Name)
		}
	case FieldNumber:
		if _, ok := v.(float64); !ok {
			return fmt.Errorf("field %q must be a number", f.Name)
		}
	case FieldBool:
		if _, ok := v.(bool); !ok {
			return fmt.Errorf("field %q must be a boolean", f.Name)
		}
	case FieldJSON:
		// any JSON value is acceptable
	}
	return nil
}

// storageValue converts a decoded JSON input value into what should be
// bound into the SQLite column for this field (bool -> 0/1, json -> its
// serialized text).
func storageValue(f Field, v any) (any, error) {
	if v == nil {
		return nil, nil
	}
	switch f.Type {
	case FieldBool:
		b := v.(bool)
		if b {
			return 1, nil
		}
		return 0, nil
	case FieldJSON:
		b, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("marshal field %q: %w", f.Name, err)
		}
		return string(b), nil
	default:
		return v, nil
	}
}

func createRecord(ctx context.Context, sqlDB *sql.DB, c *collection, input map[string]any, ownerID string) (map[string]any, error) {
	id := uuid.NewString()

	cols := []string{"id", "owner_id"}
	placeholders := []string{"?", "?"}
	args := []any{id, nullableString(ownerID)}

	for _, f := range c.Schema.Fields {
		v, present := input[f.Name]
		if !present {
			continue
		}
		sv, err := storageValue(f, v)
		if err != nil {
			return nil, err
		}
		cols = append(cols, f.Name)
		placeholders = append(placeholders, "?")
		args = append(args, sv)
	}

	stmt := fmt.Sprintf("INSERT INTO %q (%s) VALUES (%s)",
		c.Name, quoteIdentList(cols), strings.Join(placeholders, ", "))
	if _, err := sqlDB.ExecContext(ctx, stmt, args...); err != nil {
		return nil, fmt.Errorf("insert record: %w", err)
	}

	return getRecord(ctx, sqlDB, c, id)
}

func getRecord(ctx context.Context, sqlDB *sql.DB, c *collection, id string) (map[string]any, error) {
	stmt := fmt.Sprintf("SELECT %s FROM %q WHERE id = ?", selectColumns(c.Schema), c.Name)
	rows, err := sqlDB.QueryContext(ctx, stmt, id)
	if err != nil {
		return nil, fmt.Errorf("select record: %w", err)
	}
	defer rows.Close()

	recs, err := scanRecords(rows, c.Schema)
	if err != nil {
		return nil, err
	}
	if len(recs) == 0 {
		return nil, sql.ErrNoRows
	}
	return recs[0], nil
}

func updateRecord(ctx context.Context, sqlDB *sql.DB, c *collection, id string, input map[string]any) (map[string]any, error) {
	var sets []string
	var args []any
	for _, f := range c.Schema.Fields {
		v, present := input[f.Name]
		if !present {
			continue
		}
		sv, err := storageValue(f, v)
		if err != nil {
			return nil, err
		}
		sets = append(sets, fmt.Sprintf("%q = ?", f.Name))
		args = append(args, sv)
	}
	sets = append(sets, "updated = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')")

	stmt := fmt.Sprintf("UPDATE %q SET %s WHERE id = ?", c.Name, strings.Join(sets, ", "))
	args = append(args, id)

	res, err := sqlDB.ExecContext(ctx, stmt, args...)
	if err != nil {
		return nil, fmt.Errorf("update record: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("rows affected: %w", err)
	}
	if affected == 0 {
		return nil, sql.ErrNoRows
	}
	return getRecord(ctx, sqlDB, c, id)
}

func deleteRecord(ctx context.Context, sqlDB *sql.DB, c *collection, id string) error {
	res, err := sqlDB.ExecContext(ctx, fmt.Sprintf("DELETE FROM %q WHERE id = ?", c.Name), id)
	if err != nil {
		return fmt.Errorf("delete record: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// listRecords returns up to params.limit+1 records (the extra row signals
// whether a next page exists) matching the given filters, newest-first
// unless params.descending is false.
func listRecords(ctx context.Context, sqlDB *sql.DB, c *collection, params recordListParams) ([]map[string]any, error) {
	var where []string
	var args []any

	for field, val := range params.filters {
		where = append(where, fmt.Sprintf("%q = ?", field))
		args = append(args, val)
	}

	op := "<"
	order := "DESC"
	if !params.descending {
		op = ">"
		order = "ASC"
	}
	if params.cursorTime != "" {
		where = append(where, fmt.Sprintf("(created, id) %s (?, ?)", op))
		args = append(args, params.cursorTime, params.cursorID)
	}

	stmt := fmt.Sprintf("SELECT %s FROM %q", selectColumns(c.Schema), c.Name)
	if len(where) > 0 {
		stmt += " WHERE " + strings.Join(where, " AND ")
	}
	stmt += fmt.Sprintf(" ORDER BY created %s, id %s LIMIT ?", order, order)
	args = append(args, params.limit+1)

	rows, err := sqlDB.QueryContext(ctx, stmt, args...)
	if err != nil {
		return nil, fmt.Errorf("list records: %w", err)
	}
	defer rows.Close()

	return scanRecords(rows, c.Schema)
}

func selectColumns(schema Schema) string {
	cols := []string{"id", "owner_id", "created", "updated"}
	for _, f := range schema.Fields {
		cols = append(cols, f.Name)
	}
	return quoteIdentList(cols)
}

func quoteIdentList(idents []string) string {
	quoted := make([]string, len(idents))
	for i, id := range idents {
		quoted[i] = fmt.Sprintf("%q", id)
	}
	return strings.Join(quoted, ", ")
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// encodeCursor/decodeCursor implement the opaque cursor used for keyset
// pagination, encoding the last row's (created, id) tuple.
func encodeCursor(created, id string) string {
	return created + "_" + id
}

func decodeCursor(cursor string) (created, id string, ok bool) {
	i := strings.LastIndex(cursor, "_")
	if i < 0 {
		return "", "", false
	}
	created, id = cursor[:i], cursor[i+1:]
	if created == "" || id == "" {
		return "", "", false
	}
	// Sanity-check created looks like a timestamp so a malformed cursor
	// fails fast instead of silently returning an empty/wrong page.
	if _, err := time.Parse(time.RFC3339Nano, created); err != nil {
		return "", "", false
	}
	return created, id, true
}
