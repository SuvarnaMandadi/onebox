package server

import (
	"fmt"
	"regexp"
)

// FieldType is one of the small set of column types a collection field can
// declare. This intentionally stays minimal for v0.1 — no relations, no
// enums — matching the anti-scope note to keep the schema engine tiny.
type FieldType string

const (
	FieldText   FieldType = "text"
	FieldNumber FieldType = "number"
	FieldBool   FieldType = "bool"
	FieldDate   FieldType = "date"
	FieldJSON   FieldType = "json"
)

var validFieldTypes = map[FieldType]bool{
	FieldText:   true,
	FieldNumber: true,
	FieldBool:   true,
	FieldDate:   true,
	FieldJSON:   true,
}

// sqliteType returns the column type affinity to use for a field type.
func (t FieldType) sqliteType() string {
	switch t {
	case FieldNumber:
		return "REAL"
	case FieldBool:
		return "INTEGER"
	default: // text, date, json all store as text
		return "TEXT"
	}
}

// Field describes one user-defined column in a collection.
type Field struct {
	Name     string    `json:"name"`
	Type     FieldType `json:"type"`
	Required bool      `json:"required"`
}

// Schema is the JSON-defined shape of a collection's user fields.
type Schema struct {
	Fields []Field `json:"fields"`
}

// Rules controls who can list/view/create/update/delete records in a
// collection. Each action maps to one of a tiny set of rule kinds — no
// expression parser yet, per the roadmap's "start tiny" guidance.
type Rules struct {
	List   RuleKind `json:"list"`
	View   RuleKind `json:"view"`
	Create RuleKind `json:"create"`
	Update RuleKind `json:"update"`
	Delete RuleKind `json:"delete"`
}

// RuleKind is one access rule for one action on a collection.
type RuleKind string

const (
	// RulePublic allows any request, authenticated or not.
	RulePublic RuleKind = "public"
	// RuleAuthenticated requires any valid _users session.
	RuleAuthenticated RuleKind = "authenticated"
	// RuleOwner requires a valid _users session whose id matches the
	// record's owner_id column.
	RuleOwner RuleKind = "owner"
)

var validRuleKinds = map[RuleKind]bool{
	RulePublic:        true,
	RuleAuthenticated: true,
	RuleOwner:         true,
}

// DefaultRules is applied when a collection is created without explicit
// rules: safe by default, nothing public.
func DefaultRules() Rules {
	return Rules{
		List:   RuleAuthenticated,
		View:   RuleAuthenticated,
		Create: RuleAuthenticated,
		Update: RuleOwner,
		Delete: RuleOwner,
	}
}

// nameRE allows mixed-case collection/field names (e.g. "Posts", "userEmail")
// — only the leading-letter and character-set shape matters for a valid
// SQL identifier, not case. SQLite is case-sensitive for exact-match
// lookups on TEXT columns, so a collection created as "Notes" must be
// referenced as "Notes" (not "notes") — that's normal, expected behavior,
// not something this regex needs to solve.
var nameRE = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]{0,62}$`)

// systemColumns are present on every dynamic collection table and cannot be
// redeclared as user fields.
var systemColumns = map[string]bool{
	"id":       true,
	"owner_id": true,
	"created":  true,
	"updated":  true,
}

// reservedCollectionNames are the built-in tables a collection name must
// not collide with.
var reservedCollectionNames = map[string]bool{
	"_users":       true,
	"_admins":      true,
	"_collections": true,
	"_files":       true,
	"_rag_sources": true,
	"_rag_chunks":  true,
	"_usage":       true,
	"_settings":    true,
	"_migrations":  true,
}

// ValidateCollectionName checks a proposed collection (table) name.
func ValidateCollectionName(name string) error {
	if !nameRE.MatchString(name) {
		return fmt.Errorf("collection name must match %s", nameRE.String())
	}
	if reservedCollectionNames[name] || name[0] == '_' {
		return fmt.Errorf("collection name %q is reserved", name)
	}
	return nil
}

// ValidateSchema checks field names/types and rejects collisions with
// system columns.
func ValidateSchema(schema Schema) error {
	if len(schema.Fields) == 0 {
		return fmt.Errorf("schema must declare at least one field")
	}
	seen := make(map[string]bool, len(schema.Fields))
	for _, f := range schema.Fields {
		if !nameRE.MatchString(f.Name) {
			return fmt.Errorf("field name %q must match %s", f.Name, nameRE.String())
		}
		if systemColumns[f.Name] {
			return fmt.Errorf("field name %q is reserved", f.Name)
		}
		if seen[f.Name] {
			return fmt.Errorf("duplicate field name %q", f.Name)
		}
		seen[f.Name] = true
		if !validFieldTypes[f.Type] {
			return fmt.Errorf("field %q has unknown type %q", f.Name, f.Type)
		}
	}
	return nil
}

// ValidateRules checks that every rule kind, if set, is recognized. Empty
// values are allowed and filled in with DefaultRules by the caller.
func ValidateRules(rules Rules) error {
	for _, r := range []RuleKind{rules.List, rules.View, rules.Create, rules.Update, rules.Delete} {
		if r != "" && !validRuleKinds[r] {
			return fmt.Errorf("unknown rule kind %q", r)
		}
	}
	return nil
}

// createTableSQL builds the CREATE TABLE statement for a collection's
// dynamic table: system columns first, then user fields.
func createTableSQL(collectionName string, schema Schema) string {
	stmt := fmt.Sprintf(`CREATE TABLE %q (
	id TEXT PRIMARY KEY,
	owner_id TEXT,
	created TEXT NOT NULL DEFAULT (strftime('%%Y-%%m-%%dT%%H:%%M:%%fZ', 'now')),
	updated TEXT NOT NULL DEFAULT (strftime('%%Y-%%m-%%dT%%H:%%M:%%fZ', 'now'))`, collectionName)
	for _, f := range schema.Fields {
		stmt += fmt.Sprintf(",\n\t%q %s", f.Name, f.Type.sqliteType())
	}
	stmt += "\n)"
	return stmt
}
