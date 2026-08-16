package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/mail"
	"net/url"
	"regexp"
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
	// Before the required-field check below — a Default can satisfy a
	// required field the caller never mentioned, same as any other
	// PocketBase-style backend.
	applyFieldDefaults(input, schema, forCreate)

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

// validateRelationValues checks that every relation-typed field actually
// present in input points at a record that really exists — the
// record-level counterpart to validateRelationTargets' schema-level "does
// the target COLLECTION exist" check (collections.go). Reuses
// getCollectionByName + getRecord, the exact same lookups a real
// GET .../records/{id} request performs — no parallel existence-check
// path. Only fields actually present in input are checked: a partial
// PATCH that doesn't touch a relation field leaves it alone, and a null
// value on an optional relation field is intentionally skipped here (empty
// is a valid "not related to anything" state; validateRecordInput's own
// required-field check is what rejects a null on a *required* relation
// field, same as it does for every other required field type).
func validateRelationValues(ctx context.Context, sqlDB *sql.DB, c *collection, input map[string]any) error {
	for _, f := range c.Schema.Fields {
		if f.Type != FieldRelation {
			continue
		}
		v, present := input[f.Name]
		if !present || v == nil {
			continue
		}
		id, ok := v.(string)
		if !ok || id == "" {
			return fmt.Errorf("field %q must be a non-empty record id", f.Name)
		}
		target, err := getCollectionByName(ctx, sqlDB, f.RelationCollection)
		if err != nil {
			return fmt.Errorf("field %q relates to collection %q, which does not exist", f.Name, f.RelationCollection)
		}
		if _, err := getRecord(ctx, sqlDB, target, id); err != nil {
			return fmt.Errorf("field %q references record %q in collection %q, which does not exist", f.Name, id, f.RelationCollection)
		}
	}
	return nil
}

// validateUniqueFields checks every field present in input whose schema
// declares Validation.Unique against real row data — the record-level
// counterpart to checkUniqueValue's query, called once per unique field
// the same way validateRelationValues loops its own schema-flagged
// fields. excludeID is the record being updated (empty on create), same
// convention as checkUniqueValue.
func validateUniqueFields(ctx context.Context, sqlDB *sql.DB, c *collection, excludeID string, input map[string]any) error {
	for _, f := range c.Schema.Fields {
		if f.Validation == nil || !f.Validation.Unique {
			continue
		}
		v, present := input[f.Name]
		if !present || v == nil {
			continue
		}
		if err := checkUniqueValue(ctx, sqlDB, c, f.Name, excludeID, v); err != nil {
			return err
		}
	}
	return nil
}

func checkFieldType(f Field, v any) error {
	switch f.Type {
	case FieldText:
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf("field %q must be a string", f.Name)
		}
		return checkTextValidation(f, s)
	case FieldDate:
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf("field %q must be a string", f.Name)
		}
		return checkDateValidation(f, s)
	case FieldNumber:
		n, ok := v.(float64)
		if !ok {
			return fmt.Errorf("field %q must be a number", f.Name)
		}
		return checkNumberValidation(f, n)
	case FieldBool:
		if _, ok := v.(bool); !ok {
			return fmt.Errorf("field %q must be a boolean", f.Name)
		}
	case FieldJSON:
		// any JSON value is acceptable
	case FieldRelation:
		// A relation field's value is the target record's id — a plain
		// string, same shape check as text/date above. Whether a record
		// with that id actually exists in the target collection is a data
		// question this shape-only check can't answer — see
		// validateRelationValues, which runs separately (and only for
		// create/update requests that have a real db handle in hand).
		if _, ok := v.(string); !ok {
			return fmt.Errorf("field %q must be a record id (string)", f.Name)
		}
	}
	return nil
}

// checkTextValidation enforces a FieldText field's optional Format/
// MinLength/MaxLength/Pattern rules (RC2) — friendly, specific error
// messages, since these are the ones an admin (or a user submitting a
// public-rules form) is most likely to actually hit and need to
// understand, not just "field X is invalid".
func checkTextValidation(f Field, s string) error {
	v := f.Validation
	if v == nil {
		return nil
	}
	switch v.Format {
	case "email":
		if _, err := mail.ParseAddress(s); err != nil {
			return fmt.Errorf("field %q must be a valid email address", f.Name)
		}
	case "url":
		u, err := url.ParseRequestURI(s)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return fmt.Errorf("field %q must be a valid URL (including scheme, e.g. https://)", f.Name)
		}
	}
	if v.MinLength != nil && len(s) < *v.MinLength {
		return fmt.Errorf("field %q must be at least %d character(s)", f.Name, *v.MinLength)
	}
	if v.MaxLength != nil && len(s) > *v.MaxLength {
		return fmt.Errorf("field %q must be at most %d character(s)", f.Name, *v.MaxLength)
	}
	if v.Pattern != "" {
		// Compiled fresh per call rather than cached: schema validation
		// (validateFieldValidationRules) already proved this pattern
		// compiles, and record writes are not hot enough in this engine's
		// target scale (see records.go's own brute-force-is-fine notes
		// elsewhere) to justify a pattern cache.
		re := regexp.MustCompile(v.Pattern)
		if !re.MatchString(s) {
			return fmt.Errorf("field %q does not match the required pattern", f.Name)
		}
	}
	return nil
}

// checkNumberValidation enforces a FieldNumber field's optional Min/Max.
func checkNumberValidation(f Field, n float64) error {
	v := f.Validation
	if v == nil {
		return nil
	}
	if v.Min != nil && n < *v.Min {
		return fmt.Errorf("field %q must be at least %g", f.Name, *v.Min)
	}
	if v.Max != nil && n > *v.Max {
		return fmt.Errorf("field %q must be at most %g", f.Name, *v.Max)
	}
	return nil
}

// checkDateValidation enforces a FieldDate field's optional FutureOnly/
// PastOnly — parsed as RFC 3339 (the same format created/updated columns
// already use), consistent with the rest of this codebase's date handling
// rather than inventing a second date format.
func checkDateValidation(f Field, s string) error {
	v := f.Validation
	if v == nil || (!v.FutureOnly && !v.PastOnly) {
		return nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return fmt.Errorf("field %q must be a valid RFC 3339 date/time to check future_only/past_only", f.Name)
	}
	now := time.Now()
	if v.FutureOnly && !t.After(now) {
		return fmt.Errorf("field %q must be a date in the future", f.Name)
	}
	if v.PastOnly && !t.Before(now) {
		return fmt.Errorf("field %q must be a date in the past", f.Name)
	}
	return nil
}

// checkUniqueValue enforces a field's Unique rule with a real query — no
// other row in the collection may already hold this exact value. excludeID
// is the record being updated (empty on create), so a PATCH that doesn't
// touch the unique field, or resubmits its own unchanged value, doesn't
// trip over itself.
func checkUniqueValue(ctx context.Context, sqlDB *sql.DB, c *collection, field, excludeID string, v any) error {
	query := fmt.Sprintf(`SELECT COUNT(*) FROM %q WHERE %q = ? AND id != ?`, c.Name, field)
	var count int
	if err := sqlDB.QueryRowContext(ctx, query, v, excludeID).Scan(&count); err != nil {
		return fmt.Errorf("check uniqueness of %q: %w", field, err)
	}
	if count > 0 {
		return fmt.Errorf("field %q must be unique — %q is already in use", field, fmt.Sprint(v))
	}
	return nil
}

// applyFieldDefaults fills in any field's Default value for a create
// request that omitted it entirely — never for a field explicitly sent as
// null (that's a deliberate "no value" from the caller, not "didn't
// think about it") or on update (a PATCH's whole point is partial input;
// silently injecting defaults into fields the caller never mentioned
// would turn a partial update into an unintended overwrite).
func applyFieldDefaults(input map[string]any, schema Schema, forCreate bool) {
	if !forCreate {
		return
	}
	for _, f := range schema.Fields {
		if f.Validation == nil || f.Validation.Default == nil {
			continue
		}
		if _, present := input[f.Name]; !present {
			input[f.Name] = f.Validation.Default
		}
	}
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
