package server

import (
	"context"
	"database/sql"
	"testing"

	"onebox/internal/llm"
)

// seedCollection is a small test fixture helper — creates a real
// collection via the same createCollection function the real
// POST /api/collections handler uses, so validator tests exercise
// against genuine collection-registry state, not a mock.
func seedCollection(t *testing.T, db *sql.DB, name string, fields ...Field) {
	t.Helper()
	if _, err := createCollection(context.Background(), db, name, Schema{Fields: fields}, DefaultRules()); err != nil {
		t.Fatalf("seed collection %q: %v", name, err)
	}
}

func TestValidateCreateCollectionProposal(t *testing.T) {
	_, db := newTestServer(t)
	seedCollection(t, db, "existing", Field{Name: "body", Type: FieldText})

	cases := []struct {
		name    string
		payload createCollectionPayload
		wantErr bool
	}{
		{"valid new collection", createCollectionPayload{Name: "notes", Fields: []proposedField{{Name: "body", Type: "text"}}}, false},
		{"invalid collection name", createCollectionPayload{Name: "1bad-name", Fields: []proposedField{{Name: "body", Type: "text"}}}, true},
		{"reserved collection name", createCollectionPayload{Name: "_users", Fields: []proposedField{{Name: "body", Type: "text"}}}, true},
		{"unsupported field type", createCollectionPayload{Name: "notes", Fields: []proposedField{{Name: "body", Type: "markdown"}}}, true},
		{"field name collides with system column", createCollectionPayload{Name: "notes", Fields: []proposedField{{Name: "id", Type: "text"}}}, true},
		{"duplicate field name", createCollectionPayload{Name: "notes", Fields: []proposedField{{Name: "body", Type: "text"}, {Name: "body", Type: "number"}}}, true},
		{"collection already exists", createCollectionPayload{Name: "existing", Fields: []proposedField{{Name: "body", Type: "text"}}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateCreateCollectionProposal(context.Background(), db, tc.payload)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
}

func TestValidateCreateCollectionProposalWrongPayloadType(t *testing.T) {
	_, db := newTestServer(t)
	if err := validateCreateCollectionProposal(context.Background(), db, "not a payload"); err == nil {
		t.Fatal("expected error for mismatched payload type")
	}
}

func TestValidateDeleteCollectionProposal(t *testing.T) {
	_, db := newTestServer(t)
	seedCollection(t, db, "existing", Field{Name: "body", Type: FieldText})

	if err := validateDeleteCollectionProposal(context.Background(), db, deleteCollectionPayload{Name: "existing"}); err != nil {
		t.Fatalf("expected existing collection to validate, got %v", err)
	}
	if err := validateDeleteCollectionProposal(context.Background(), db, deleteCollectionPayload{Name: "missing"}); err == nil {
		t.Fatal("expected error deleting a nonexistent collection")
	}
}

func TestValidateRenameCollectionProposal(t *testing.T) {
	_, db := newTestServer(t)
	seedCollection(t, db, "orders", Field{Name: "total", Type: FieldNumber})
	seedCollection(t, db, "purchases", Field{Name: "total", Type: FieldNumber})

	cases := []struct {
		name    string
		payload renameCollectionPayload
		wantErr bool
	}{
		{"valid rename", renameCollectionPayload{From: "orders", To: "sales"}, false},
		{"source does not exist", renameCollectionPayload{From: "missing", To: "sales"}, true},
		{"target already exists", renameCollectionPayload{From: "orders", To: "purchases"}, true},
		{"rename to self", renameCollectionPayload{From: "orders", To: "orders"}, true},
		{"invalid target name", renameCollectionPayload{From: "orders", To: "1bad"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateRenameCollectionProposal(context.Background(), db, tc.payload)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
}

func TestValidateAddFieldProposal(t *testing.T) {
	_, db := newTestServer(t)
	seedCollection(t, db, "users", Field{Name: "email", Type: FieldText})

	cases := []struct {
		name    string
		payload addFieldPayload
		wantErr bool
	}{
		{"valid new field", addFieldPayload{Collection: "users", Field: "age", Type: "number"}, false},
		{"collection does not exist", addFieldPayload{Collection: "missing", Field: "age", Type: "number"}, true},
		{"field already exists", addFieldPayload{Collection: "users", Field: "email", Type: "text"}, true},
		{"unsupported type", addFieldPayload{Collection: "users", Field: "age", Type: "float"}, true},
		{"invalid field name", addFieldPayload{Collection: "users", Field: "2bad", Type: "text"}, true},
		{"field name collides with system column", addFieldPayload{Collection: "users", Field: "owner_id", Type: "text"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateAddFieldProposal(context.Background(), db, tc.payload)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
}

func TestValidateDeleteFieldProposal(t *testing.T) {
	_, db := newTestServer(t)
	seedCollection(t, db, "users", Field{Name: "email", Type: FieldText})

	cases := []struct {
		name    string
		payload deleteFieldPayload
		wantErr bool
	}{
		{"valid existing field", deleteFieldPayload{Collection: "users", Field: "email"}, false},
		{"field does not exist", deleteFieldPayload{Collection: "users", Field: "phone"}, true},
		{"collection does not exist", deleteFieldPayload{Collection: "missing", Field: "email"}, true},
		{"system column is never a real field", deleteFieldPayload{Collection: "users", Field: "id"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateDeleteFieldProposal(context.Background(), db, tc.payload)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
}

func TestValidateImportDataProposal(t *testing.T) {
	_, db := newTestServer(t)
	seedCollection(t, db, "orders", Field{Name: "total", Type: FieldNumber})

	if err := validateImportDataProposal(context.Background(), db, importDataPayload{Collection: "orders"}); err != nil {
		t.Fatalf("expected existing collection to validate, got %v", err)
	}
	if err := validateImportDataProposal(context.Background(), db, importDataPayload{Collection: "missing"}); err == nil {
		t.Fatal("expected error importing into a nonexistent collection")
	}
}

func TestValidateUpdateSchemaProposal(t *testing.T) {
	_, db := newTestServer(t)
	seedCollection(t, db, "orders", Field{Name: "total", Type: FieldNumber})

	if err := validateUpdateSchemaProposal(context.Background(), db, updateSchemaPayload{Collection: "orders", Summary: "widen total"}); err != nil {
		t.Fatalf("expected existing collection to validate, got %v", err)
	}
	if err := validateUpdateSchemaProposal(context.Background(), db, updateSchemaPayload{Collection: "missing", Summary: "x"}); err == nil {
		t.Fatal("expected error updating the schema of a nonexistent collection")
	}
}

// TestSchemaEngineValidatorRejectsUnknownType guards the fail-closed
// default branch — a proposedAction with a Type nothing registered a
// check for must never validate successfully by accident.
func TestSchemaEngineValidatorRejectsUnknownType(t *testing.T) {
	_, db := newTestServer(t)
	err := schemaEngineValidator{}.Validate(context.Background(), db, proposedAction{Type: "launch_missiles"})
	if err == nil {
		t.Fatal("expected an unregistered proposal type to fail validation")
	}
}

// TestValidateProposalsFiltersInvalidAndKeepsValid is the (*Server)
// method-level test — mixed valid/invalid proposals in, only the valid
// one survives, in the same relative order.
func TestValidateProposalsFiltersInvalidAndKeepsValid(t *testing.T) {
	srv, db := newTestServer(t)
	seedCollection(t, db, "existing", Field{Name: "body", Type: FieldText})

	actions := []proposedAction{
		{Type: actionCreateCollection, Payload: createCollectionPayload{Name: "existing", Fields: []proposedField{{Name: "body", Type: "text"}}}},
		{Type: actionCreateCollection, Payload: createCollectionPayload{Name: "notes", Fields: []proposedField{{Name: "body", Type: "text"}}}},
	}
	got := srv.validateProposals(context.Background(), actions)
	if len(got) != 1 {
		t.Fatalf("expected 1 surviving proposal, got %d: %+v", len(got), got)
	}
	payload, ok := got[0].Payload.(createCollectionPayload)
	if !ok || payload.Name != "notes" {
		t.Fatalf("unexpected surviving proposal: %+v", got[0])
	}
}

// TestNativeToolCallParserRejectsUnknownProperties pins strictUnmarshal's
// contract: a tool call whose arguments include a property the schema
// never declared must be dropped rather than silently ignored — "reject
// unknown properties" from the product spec, enforced structurally.
func TestNativeToolCallParserRejectsUnknownProperties(t *testing.T) {
	call := toolCall(actionDeleteCollection, map[string]any{"name": "orders", "cascade": true})
	actions := nativeToolCallParser{}.ParseActions(llm.ChatResult{ToolCalls: []llm.ToolCall{call}})
	if len(actions) != 0 {
		t.Fatalf("expected a call with an undeclared property to be dropped, got %+v", actions)
	}
}
