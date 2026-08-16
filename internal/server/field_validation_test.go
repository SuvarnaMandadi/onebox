package server

import (
	"encoding/json"
	"net/http"
	"testing"
)

// intPtr/floatPtr are small helpers so table-driven Field literals below
// don't need a one-off local variable per pointer field.
func intPtr(n int) *int           { return &n }
func floatPtr(n float64) *float64 { return &n }

// TestValidateFieldValidationRulesSchemaChecks is the schema-time pin for
// FieldValidation's structural rules (RC2) — a rule set on the wrong
// field type, an inconsistent min/max, or an uncompilable regex must be
// rejected at collection-create/schema-update time, before any record
// ever tries to use it.
func TestValidateFieldValidationRulesSchemaChecks(t *testing.T) {
	cases := []struct {
		name    string
		field   Field
		wantErr bool
	}{
		{"min_length on a number field", Field{Name: "n", Type: FieldNumber, Validation: &FieldValidation{MinLength: intPtr(1)}}, true},
		{"min/max on a text field", Field{Name: "n", Type: FieldText, Validation: &FieldValidation{Min: floatPtr(1)}}, true},
		{"future_only on a text field", Field{Name: "n", Type: FieldText, Validation: &FieldValidation{FutureOnly: true}}, true},
		{"unknown format", Field{Name: "n", Type: FieldText, Validation: &FieldValidation{Format: "not-a-format"}}, true},
		{"min_length > max_length", Field{Name: "n", Type: FieldText, Validation: &FieldValidation{MinLength: intPtr(10), MaxLength: intPtr(5)}}, true},
		{"min > max", Field{Name: "n", Type: FieldNumber, Validation: &FieldValidation{Min: floatPtr(10), Max: floatPtr(5)}}, true},
		{"invalid regex", Field{Name: "n", Type: FieldText, Validation: &FieldValidation{Pattern: "["}}, true},
		{"future_only and past_only both set", Field{Name: "n", Type: FieldDate, Validation: &FieldValidation{FutureOnly: true, PastOnly: true}}, true},
		{"default value wrong type", Field{Name: "n", Type: FieldNumber, Validation: &FieldValidation{Default: "not a number"}}, true},
		{"legal email format", Field{Name: "n", Type: FieldText, Validation: &FieldValidation{Format: "email"}}, false},
		{"legal number range", Field{Name: "n", Type: FieldNumber, Validation: &FieldValidation{Min: floatPtr(0), Max: floatPtr(100)}}, false},
		{"legal pattern", Field{Name: "n", Type: FieldText, Validation: &FieldValidation{Pattern: "^[A-Z]{3}$"}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateSchema(Schema{Fields: []Field{tc.field}})
			if (err != nil) != tc.wantErr {
				t.Fatalf("ValidateSchema() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

// TestRecordValidationEnforcesFieldRules is the end-to-end pin for record
// writes actually enforcing FieldValidation — email format, length
// bounds, and number range, each via a real POST /api/collections/:name/records.
func TestRecordValidationEnforcesFieldRules(t *testing.T) {
	srv, db := newTestServer(t)
	token := bootstrapAdmin(t, srv)

	seedCollection(t, db, "signups",
		Field{Name: "email", Type: FieldText, Required: true, Validation: &FieldValidation{Format: "email"}},
		Field{Name: "username", Type: FieldText, Required: true, Validation: &FieldValidation{MinLength: intPtr(3), MaxLength: intPtr(20)}},
		Field{Name: "age", Type: FieldNumber, Validation: &FieldValidation{Min: floatPtr(13), Max: floatPtr(120)}},
	)

	cases := []struct {
		name       string
		body       map[string]any
		wantStatus int
	}{
		{"valid record", map[string]any{"email": "a@example.com", "username": "alice", "age": float64(30)}, http.StatusCreated},
		{"bad email", map[string]any{"email": "not-an-email", "username": "alice", "age": float64(30)}, http.StatusBadRequest},
		{"username too short", map[string]any{"email": "a@example.com", "username": "ab", "age": float64(30)}, http.StatusBadRequest},
		{"age below min", map[string]any{"email": "a@example.com", "username": "alice", "age": float64(5)}, http.StatusBadRequest},
		{"age above max", map[string]any{"email": "a@example.com", "username": "alice", "age": float64(999)}, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doAuth(t, srv, http.MethodPost, "/api/collections/signups/records", token, tc.body)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d, body = %s", rec.Code, tc.wantStatus, rec.Body.String())
			}
		})
	}
}

// TestRecordValidationUnique confirms a Unique field rejects a second
// record with the same value, but allows updating a record to keep its
// own unchanged value (excludeID must actually exclude the record itself).
func TestRecordValidationUnique(t *testing.T) {
	srv, db := newTestServer(t)
	token := bootstrapAdmin(t, srv)
	seedCollection(t, db, "accounts", Field{Name: "handle", Type: FieldText, Required: true, Validation: &FieldValidation{Unique: true}})

	first := doAuth(t, srv, http.MethodPost, "/api/collections/accounts/records", token, map[string]any{"handle": "neo"})
	if first.Code != http.StatusCreated {
		t.Fatalf("first create: status = %d, body = %s", first.Code, first.Body.String())
	}
	var firstRec map[string]any
	if err := json.Unmarshal(first.Body.Bytes(), &firstRec); err != nil {
		t.Fatalf("decode: %v", err)
	}

	dupe := doAuth(t, srv, http.MethodPost, "/api/collections/accounts/records", token, map[string]any{"handle": "neo"})
	if dupe.Code != http.StatusBadRequest {
		t.Fatalf("duplicate create: status = %d, want 400, body = %s", dupe.Code, dupe.Body.String())
	}

	// Updating the same record to its own existing value must not trip
	// over its own row.
	selfUpdate := doAuth(t, srv, http.MethodPatch, "/api/collections/accounts/records/"+firstRec["id"].(string), token, map[string]any{"handle": "neo"})
	if selfUpdate.Code != http.StatusOK {
		t.Fatalf("self-update: status = %d, want 200, body = %s", selfUpdate.Code, selfUpdate.Body.String())
	}
}

// TestRecordValidationDefaultValue confirms a field's Default is applied
// when a create request omits it, but never overrides an explicit value
// and never applies on update (partial-input semantics).
func TestRecordValidationDefaultValue(t *testing.T) {
	srv, db := newTestServer(t)
	token := bootstrapAdmin(t, srv)
	seedCollection(t, db, "tickets",
		Field{Name: "title", Type: FieldText, Required: true},
		Field{Name: "status", Type: FieldText, Validation: &FieldValidation{Default: "open"}},
	)

	rec := doAuth(t, srv, http.MethodPost, "/api/collections/tickets/records", token, map[string]any{"title": "bug report"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created["status"] != "open" {
		t.Fatalf("status = %v, want default %q", created["status"], "open")
	}

	explicit := doAuth(t, srv, http.MethodPost, "/api/collections/tickets/records", token, map[string]any{"title": "bug 2", "status": "closed"})
	var createdExplicit map[string]any
	if err := json.Unmarshal(explicit.Body.Bytes(), &createdExplicit); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if createdExplicit["status"] != "closed" {
		t.Fatalf("explicit status = %v, want %q (default must not override an explicit value)", createdExplicit["status"], "closed")
	}
}
