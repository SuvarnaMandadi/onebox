package server

import (
	"fmt"
	"regexp"
	"strings"
)

// FieldType is one of the small set of column types a collection field can
// declare. This started as v0.1's deliberately tiny set — no relations, no
// enums — and stays that way except for one addition: FieldRelation
// (Milestone 6), a single-target foreign-key-like reference to another
// collection. Still no cascading deletes, no many-to-many — just "this
// field's value is the id of a record in another collection," matching the
// same start-tiny discipline the rest of this file follows.
type FieldType string

const (
	FieldText     FieldType = "text"
	FieldNumber   FieldType = "number"
	FieldBool     FieldType = "bool"
	FieldDate     FieldType = "date"
	FieldJSON     FieldType = "json"
	FieldRelation FieldType = "relation"
)

var validFieldTypes = map[FieldType]bool{
	FieldText:     true,
	FieldNumber:   true,
	FieldBool:     true,
	FieldDate:     true,
	FieldJSON:     true,
	FieldRelation: true,
}

// sqliteType returns the column type affinity to use for a field type.
func (t FieldType) sqliteType() string {
	switch t {
	case FieldNumber:
		return "REAL"
	case FieldBool:
		return "INTEGER"
	default: // text, date, json, relation all store as text (relation: the target record's id)
		return "TEXT"
	}
}

// Field describes one user-defined column in a collection.
type Field struct {
	Name     string    `json:"name"`
	Type     FieldType `json:"type"`
	Required bool      `json:"required"`
	// RenameFrom, only meaningful on a schema-update request (never
	// persisted — updateCollectionSchema strips it before saving), tells
	// the rebuild which existing column's data to carry into this field.
	// Omitted (or equal to Name) means "same field, no rename."
	RenameFrom string `json:"rename_from,omitempty"`
	// RelationCollection names the target collection a FieldRelation field
	// points at — the column's value is expected to be the id of a record
	// in that collection. Required (and validated as a legal collection
	// name) when Type is FieldRelation; must be empty for every other type
	// — see ValidateSchema. Whether the named collection actually exists,
	// and whether any given value is really a record in it, are both data
	// questions this purely structural type can't answer — see
	// validateRelationTargets (collections.go, schema-level: does the
	// target collection exist) and validateRelationValues (records.go,
	// record-level: does the referenced record exist).
	RelationCollection string `json:"relation_collection,omitempty"`
	// Validation holds this field's optional, type-appropriate validation
	// rules (RC2) — nil/zero means "no extra rules beyond Required and the
	// base type check". See FieldValidation's own doc comment for exactly
	// which rules apply to which FieldType.
	Validation *FieldValidation `json:"validation,omitempty"`
}

// FieldValidation is the schema-declared, per-field validation rules RC2
// asks for (PocketBase-style: email/URL format, length/range bounds, a
// regex pattern, uniqueness, a default value) — deliberately modeled as
// extra rules on the existing small FieldType set (text/number/date/...)
// rather than a growing zoo of new types (Email, Phone, Username,
// Password, ...), matching this schema engine's established "start tiny"
// discipline (see FieldType's own doc comment). Every rule here is
// optional and only meaningful on the field type it names — ValidateSchema
// rejects a rule set on the wrong type (e.g. MinLength on a number field)
// the same way it already rejects a relation_collection on a non-relation
// field, so a schema can never carry a rule that silently does nothing.
type FieldValidation struct {
	// Format further constrains a text field's shape beyond "is a
	// string" — "" (none), "email", or "url". Phone/username/password
	// (RC2 also names these) aren't distinct validated formats yet — a
	// phone number's legal shape varies too much by country to check
	// meaningfully without a much bigger library than this schema engine
	// wants to depend on, and username/password are usually product-
	// specific policy (min length, allowed characters) better expressed
	// via MinLength/MaxLength/Pattern below than a fixed built-in rule.
	Format string `json:"format,omitempty"`
	// MinLength/MaxLength bound a text field's character count.
	MinLength *int `json:"min_length,omitempty"`
	MaxLength *int `json:"max_length,omitempty"`
	// Pattern is a regular expression a text field's value must fully
	// match (anchored automatically — the admin writes just the pattern
	// body, e.g. "[A-Z]{3}-[0-9]+", not "^...$").
	Pattern string `json:"pattern,omitempty"`
	// Min/Max bound a number field's value (inclusive).
	Min *float64 `json:"min,omitempty"`
	Max *float64 `json:"max,omitempty"`
	// FutureOnly/PastOnly constrain a date field relative to the moment
	// it's written — mutually exclusive (ValidateSchema rejects both set).
	FutureOnly bool `json:"future_only,omitempty"`
	PastOnly   bool `json:"past_only,omitempty"`
	// Unique requires no other record in the same collection to already
	// have this exact value — checked with a real query at write time
	// (see checkUniqueValue in records.go), not just structurally.
	Unique bool `json:"unique,omitempty"`
	// Default is applied when a create request omits this field entirely
	// (not when it's explicitly sent as null) — see applyFieldDefaults in
	// records.go. Stored/compared as a decoded JSON value so it can hold
	// whatever shape the field type expects (a string, a number, a bool).
	Default any `json:"default,omitempty"`
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

// SlugifyCollectionName turns a human-typed name ("Customer Details") into
// a legal internal identifier ("customer_details") — lowercased, with any
// run of whitespace/punctuation collapsed to a single underscore, so a
// user never has to manually remove spaces themselves (RC2). Applied by
// createCollection before ValidateCollectionName, so this is the one place
// that decides what "the name" actually becomes; every caller (the REST
// handler, the AI's create_collection tool, tests) goes through it
// identically rather than each reimplementing its own cleanup. A name
// that's already a legal identifier passes through unchanged (case
// preserved) — this only transforms what nameRE would otherwise reject,
// it doesn't force a casing convention on an already-valid name.
func SlugifyCollectionName(name string) string {
	if nameRE.MatchString(name) {
		return name
	}
	var b strings.Builder
	prevUnderscore := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			prevUnderscore = false
		case !prevUnderscore && b.Len() > 0:
			b.WriteByte('_')
			prevUnderscore = true
		}
	}
	slug := strings.TrimRight(b.String(), "_")
	// nameRE requires a leading letter — a name that was all digits/
	// punctuation ("2024", "!!!") produces an empty or digit-led slug that
	// still can't be a valid identifier; leave it as-is and let the
	// existing "must match" error explain why, rather than silently
	// inventing a prefix that hides what the admin actually typed.
	if slug == "" || (slug[0] >= '0' && slug[0] <= '9') {
		return name
	}
	if len(slug) > 63 {
		slug = strings.TrimRight(slug[:63], "_")
	}
	// A leading underscore (or run of them) is exactly the one character
	// class this loop can never preserve — nameRE requires a leading
	// letter, full stop — which means "_users" would otherwise silently
	// slugify to "users" and sail past the reserved-name check as if it
	// were an unrelated, perfectly legal name. Stripping punctuation is
	// meant to turn "Customer Details" into "customer_details", not to
	// launder a reserved/system name into an admin-owned one — refuse the
	// transform here and let the original name hit ValidateCollectionName
	// unchanged, so it fails loudly with "reserved" instead of succeeding
	// silently as something else.
	if reservedCollectionNames["_"+slug] {
		return name
	}
	return slug
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
		// relation_collection is only meaningful (and only checked here for
		// syntactic legality — actual existence is a data question, see
		// validateRelationTargets in collections.go) on a relation field;
		// on any other type it's just a stray value nobody asked for, and
		// silently keeping it around risks the UI/AI treating a non-relation
		// field as if it pointed somewhere.
		switch {
		case f.Type == FieldRelation && f.RelationCollection == "":
			return fmt.Errorf("field %q is a relation but names no target collection", f.Name)
		case f.Type == FieldRelation && !nameRE.MatchString(f.RelationCollection):
			return fmt.Errorf("field %q relates to an invalid collection name %q", f.Name, f.RelationCollection)
		case f.Type != FieldRelation && f.RelationCollection != "":
			return fmt.Errorf("field %q: relation_collection is only valid on a relation field", f.Name)
		}
		if err := validateFieldValidationRules(f); err != nil {
			return err
		}
	}
	return nil
}

var validFormats = map[string]bool{"": true, "email": true, "url": true}

// validateFieldValidationRules checks Field.Validation's structural
// legality — every rule is only meaningful on the type it names (a
// MinLength on a number field would just silently never fire), and a
// handful of rules need internal consistency checks (min <= max, a
// compiling regex) independent of any actual record data. This is the
// schema-time half; checkFieldType/checkUniqueValue (records.go) are the
// record-time half that enforce these rules against real values.
func validateFieldValidationRules(f Field) error {
	v := f.Validation
	if v == nil {
		return nil
	}
	if v.Format != "" || v.MinLength != nil || v.MaxLength != nil || v.Pattern != "" {
		if f.Type != FieldText {
			return fmt.Errorf("field %q: format/min_length/max_length/pattern only apply to a text field", f.Name)
		}
	}
	if !validFormats[v.Format] {
		return fmt.Errorf("field %q: unknown validation format %q", f.Name, v.Format)
	}
	if v.MinLength != nil && *v.MinLength < 0 {
		return fmt.Errorf("field %q: min_length must not be negative", f.Name)
	}
	if v.MinLength != nil && v.MaxLength != nil && *v.MinLength > *v.MaxLength {
		return fmt.Errorf("field %q: min_length must not exceed max_length", f.Name)
	}
	if v.Pattern != "" {
		if _, err := regexp.Compile(v.Pattern); err != nil {
			return fmt.Errorf("field %q: invalid pattern: %w", f.Name, err)
		}
	}
	if (v.Min != nil || v.Max != nil) && f.Type != FieldNumber {
		return fmt.Errorf("field %q: min/max only apply to a number field", f.Name)
	}
	if v.Min != nil && v.Max != nil && *v.Min > *v.Max {
		return fmt.Errorf("field %q: min must not exceed max", f.Name)
	}
	if (v.FutureOnly || v.PastOnly) && f.Type != FieldDate {
		return fmt.Errorf("field %q: future_only/past_only only apply to a date field", f.Name)
	}
	if v.FutureOnly && v.PastOnly {
		return fmt.Errorf("field %q: future_only and past_only are mutually exclusive", f.Name)
	}
	if v.Default != nil {
		if err := checkFieldType(f, v.Default); err != nil {
			return fmt.Errorf("field %q: default value %w", f.Name, err)
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
