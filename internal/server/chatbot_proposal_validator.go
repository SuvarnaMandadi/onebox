package server

import (
	"context"
	"database/sql"
	"fmt"
	"log"
)

// ProposalValidator is the mandatory checkpoint between a structurally
// parsed proposedAction (ActionParser's output — see chatbot_actions.go)
// and it becoming a rendered Proposal Card:
//
//	LLM -> ToolCall -> ActionParser -> ProposalValidator ->
//	Validated Proposal -> Proposal Card -> (future) Approval -> (future) Execution
//
// ActionParser already guarantees a proposal's JSON *shape* matches its
// tool's declared schema — required fields present, no unknown
// properties, well-formed JSON (see nativeToolCallParser and
// compatNormalizingParser). ProposalValidator is the next, separate
// concern: whether the operation is actually legal against this
// instance's real state and OneBox's own domain rules — collection/field
// name syntax, supported field types, collection existence, name
// collisions, impossible operations (renaming a collection to itself,
// deleting a field that isn't there). These are exactly the questions a
// real POST /api/collections or PUT /api/collections/{name}/schema
// request has to answer, so every check below calls the same
// ValidateCollectionName / ValidateSchema / ValidateRules functions
// (collection_schema.go) and the same collection-registry lookups
// (collections.go, principally getCollectionByName) those real endpoints
// already use. Nothing here is a second, drifted copy of "what's a legal
// field name" — a proposal and a hand-typed API request are held to
// identical standards by construction. The model's own claims about any
// of this are never trusted on their own.
//
// A proposal that fails validation is dropped, not rendered as a broken
// or "invalid" card — see (*Server).validateProposals.
type ProposalValidator interface {
	Validate(ctx context.Context, db *sql.DB, a proposedAction) error
}

// schemaEngineValidator is the only ProposalValidator implementation.
// Unlike ActionParser, a proposal's legality doesn't depend on which LLM
// produced it — the same collection either does or doesn't already
// exist — so this has no need for compatNormalizingParser's kind of
// swappable-implementation treatment.
type schemaEngineValidator struct{}

func (schemaEngineValidator) Validate(ctx context.Context, db *sql.DB, a proposedAction) error {
	switch a.Type {
	case actionCreateCollection:
		return validateCreateCollectionProposal(ctx, db, a.Payload)
	case actionDeleteCollection:
		return validateDeleteCollectionProposal(ctx, db, a.Payload)
	case actionRenameCollection:
		return validateRenameCollectionProposal(ctx, db, a.Payload)
	case actionAddField:
		return validateAddFieldProposal(ctx, db, a.Payload)
	case actionDeleteField:
		return validateDeleteFieldProposal(ctx, db, a.Payload)
	case actionImportData:
		return validateImportDataProposal(ctx, db, a.Payload)
	case actionUpdateSchema:
		return validateUpdateSchemaProposal(ctx, db, a.Payload)
	case actionDescribeOnebox:
		return validateDescribeOneboxProposal(a.Payload)
	case actionListCollections:
		return validateListCollectionsProposal(a.Payload)
	case actionListRecords:
		return validateListRecordsProposal(ctx, db, a.Payload)
	case actionFindRelatedRecords:
		return validateFindRelatedRecordsProposal(ctx, db, a.Payload)
	case actionListBackups:
		return validateListBackupsProposal(a.Payload)
	case actionGetRecentErrors:
		return validateGetRecentErrorsProposal(a.Payload)
	case actionGetSettingsSummary:
		return validateGetSettingsSummaryProposal(a.Payload)
	default:
		// actionMeta (chatbot_actions.go) is the only source of a.Type
		// values ActionParser ever produces, and every entry in it is
		// handled above — this is unreachable in practice. Fail closed
		// rather than let a type nobody wrote a check for slip through
		// unvalidated if that invariant is ever broken.
		return fmt.Errorf("no validator registered for proposal type %q", a.Type)
	}
}

// requireExists loads a collection a proposal claims to operate on,
// turning the registry's own errCollectionNotFound into a validation
// failure with a message that names the actual problem, and treating any
// other lookup error as equally disqualifying (fail closed: a proposal
// never becomes a card when its precondition couldn't even be checked).
func requireExists(ctx context.Context, db *sql.DB, name string) (*collection, error) {
	c, err := getCollectionByName(ctx, db, name)
	if err == errCollectionNotFound {
		return nil, fmt.Errorf("collection %q does not exist", name)
	}
	if err != nil {
		return nil, fmt.Errorf("checking collection %q: %w", name, err)
	}
	return c, nil
}

// requireNotExists is requireExists' complement, for operations that
// would collide with an existing collection (create, or a rename's
// target name).
func requireNotExists(ctx context.Context, db *sql.DB, name string) error {
	if _, err := getCollectionByName(ctx, db, name); err == nil {
		return fmt.Errorf("collection %q already exists", name)
	} else if err != errCollectionNotFound {
		return fmt.Errorf("checking collection %q: %w", name, err)
	}
	return nil
}

func validateCreateCollectionProposal(ctx context.Context, db *sql.DB, payload any) error {
	p, ok := payload.(createCollectionPayload)
	if !ok {
		return fmt.Errorf("internal: create_collection payload has wrong type %T", payload)
	}
	if err := ValidateCollectionName(p.Name); err != nil {
		return err
	}
	fields := make([]Field, len(p.Fields))
	for i, f := range p.Fields {
		fields[i] = Field{Name: f.Name, Type: FieldType(f.Type), Required: f.Required, RelationCollection: f.RelationCollection, Validation: f.Validation}
	}
	if err := ValidateSchema(Schema{Fields: fields}); err != nil {
		return err
	}
	// RC3: relation_collection/validation are now real, model-proposable
	// field attributes (see proposedField's doc comment) — validate them
	// against real instance state the same way validateRelationTargets
	// already does for a real POST /api/collections request, so a
	// proposal naming a nonexistent relation target is rejected here,
	// not left to fail later inside executeCreateCollection.
	if err := validateRelationTargets(ctx, db, Schema{Fields: fields}); err != nil {
		return err
	}
	return requireNotExists(ctx, db, p.Name)
}

func validateDeleteCollectionProposal(ctx context.Context, db *sql.DB, payload any) error {
	p, ok := payload.(deleteCollectionPayload)
	if !ok {
		return fmt.Errorf("internal: delete_collection payload has wrong type %T", payload)
	}
	_, err := requireExists(ctx, db, p.Name)
	return err
}

func validateRenameCollectionProposal(ctx context.Context, db *sql.DB, payload any) error {
	p, ok := payload.(renameCollectionPayload)
	if !ok {
		return fmt.Errorf("internal: rename_collection payload has wrong type %T", payload)
	}
	if p.From == p.To {
		return fmt.Errorf("cannot rename collection %q to itself", p.From)
	}
	if err := ValidateCollectionName(p.To); err != nil {
		return err
	}
	if _, err := requireExists(ctx, db, p.From); err != nil {
		return err
	}
	return requireNotExists(ctx, db, p.To)
}

func validateAddFieldProposal(ctx context.Context, db *sql.DB, payload any) error {
	p, ok := payload.(addFieldPayload)
	if !ok {
		return fmt.Errorf("internal: add_field payload has wrong type %T", payload)
	}
	existing, err := requireExists(ctx, db, p.Collection)
	if err != nil {
		return err
	}
	// Validated as "the resulting schema," not the new field in isolation —
	// the same duplicate-name/system-column/unknown-type checks
	// ValidateSchema already runs for a real PUT .../schema request apply
	// identically here, with zero duplicated rules.
	hypothetical := make([]Field, 0, len(existing.Schema.Fields)+1)
	hypothetical = append(hypothetical, existing.Schema.Fields...)
	newField := Field{Name: p.Field, Type: FieldType(p.Type), Required: p.Required, RelationCollection: p.RelationCollection, Validation: p.Validation}
	hypothetical = append(hypothetical, newField)
	if err := ValidateSchema(Schema{Fields: hypothetical}); err != nil {
		return err
	}
	// See validateCreateCollectionProposal's identical comment — a
	// relation_collection is now something add_field can propose too.
	return validateRelationTargets(ctx, db, Schema{Fields: []Field{newField}})
}

func validateDeleteFieldProposal(ctx context.Context, db *sql.DB, payload any) error {
	p, ok := payload.(deleteFieldPayload)
	if !ok {
		return fmt.Errorf("internal: delete_field payload has wrong type %T", payload)
	}
	existing, err := requireExists(ctx, db, p.Collection)
	if err != nil {
		return err
	}
	for _, f := range existing.Schema.Fields {
		if f.Name == p.Field {
			return nil
		}
	}
	// Also the correct outcome for a model that named a system column
	// (id/owner_id/created/updated) — those are never in Schema.Fields, so
	// this same "not found" path rejects that impossible operation too.
	return fmt.Errorf("field %q does not exist on collection %q", p.Field, p.Collection)
}

func validateImportDataProposal(ctx context.Context, db *sql.DB, payload any) error {
	p, ok := payload.(importDataPayload)
	if !ok {
		return fmt.Errorf("internal: import_data payload has wrong type %T", payload)
	}
	_, err := requireExists(ctx, db, p.Collection)
	return err
}

func validateUpdateSchemaProposal(ctx context.Context, db *sql.DB, payload any) error {
	p, ok := payload.(updateSchemaPayload)
	if !ok {
		return fmt.Errorf("internal: update_schema payload has wrong type %T", payload)
	}
	// update_schema's payload is deliberately just {collection, summary} —
	// no structured field list to run through ValidateSchema (see
	// updateSchemaPayload's doc comment in chatbot_actions.go) — so
	// existence is the only thing there is to check today.
	_, err := requireExists(ctx, db, p.Collection)
	return err
}

// validateDescribeOneboxProposal/validateListCollectionsProposal have
// nothing to check against real instance state — unlike every proposal
// above, which claims to operate on a specific named collection/field that
// may or may not exist, these two read-only tools take no arguments at all
// (see describeOneboxPayload/listCollectionsPayload) and are always legal
// to run. They're only wired into this switch at all so the "no validator
// registered" fail-closed default above never applies to them — an
// omission here would silently drop "What is OneBox?"/"List my
// collections" from ever executing, not because the operation is
// dangerous, but because a case was missing.
func validateDescribeOneboxProposal(payload any) error {
	if _, ok := payload.(describeOneboxPayload); !ok {
		return fmt.Errorf("internal: describe_onebox payload has wrong type %T", payload)
	}
	return nil
}

func validateListCollectionsProposal(payload any) error {
	if _, ok := payload.(listCollectionsPayload); !ok {
		return fmt.Errorf("internal: list_collections payload has wrong type %T", payload)
	}
	return nil
}

// validateListBackupsProposal/validateGetRecentErrorsProposal/
// validateGetSettingsSummaryProposal (RC4) are the same trivial
// shape-only check as validateDescribeOneboxProposal/
// validateListCollectionsProposal above — all three new tools take no
// arguments and touch no collection registry state, so there's nothing
// domain-specific to check beyond "ActionParser really did build the
// payload type this action's own actionMeta entry expects."
func validateListBackupsProposal(payload any) error {
	if _, ok := payload.(listBackupsPayload); !ok {
		return fmt.Errorf("internal: list_backups payload has wrong type %T", payload)
	}
	return nil
}

func validateGetRecentErrorsProposal(payload any) error {
	if _, ok := payload.(getRecentErrorsPayload); !ok {
		return fmt.Errorf("internal: get_recent_errors payload has wrong type %T", payload)
	}
	return nil
}

func validateGetSettingsSummaryProposal(payload any) error {
	if _, ok := payload.(getSettingsSummaryPayload); !ok {
		return fmt.Errorf("internal: get_settings_summary payload has wrong type %T", payload)
	}
	return nil
}

func validateListRecordsProposal(ctx context.Context, db *sql.DB, payload any) error {
	p, ok := payload.(listRecordsPayload)
	if !ok {
		return fmt.Errorf("internal: list_records payload has wrong type %T", payload)
	}
	_, err := requireExists(ctx, db, p.Collection)
	return err
}

// validateFindRelatedRecordsProposal only checks that the starting
// collection exists — same as validateListRecordsProposal, and for the
// same reason (see requireExists). Whether record_id actually names a real
// record is deliberately NOT checked here: that's a much cheaper read to
// defer to execution time (executeFindRelatedRecords,
// chatbot_tool_execution.go), which already reports a clean "not found"
// result rather than erroring — no need to look it up twice.
func validateFindRelatedRecordsProposal(ctx context.Context, db *sql.DB, payload any) error {
	p, ok := payload.(findRelatedRecordsPayload)
	if !ok {
		return fmt.Errorf("internal: find_related_records payload has wrong type %T", payload)
	}
	_, err := requireExists(ctx, db, p.Collection)
	return err
}

// proposalValidator is the single point (*Server).validateProposals goes
// through — a package-level var, same pattern as actionParser, so a
// future test or a different validation strategy can swap it without
// touching any caller.
var proposalValidator ProposalValidator = schemaEngineValidator{}

// validateProposals runs every candidate proposedAction through
// proposalValidator and keeps only the ones that pass — see
// ProposalValidator's doc comment for why this is a mandatory step
// between ActionParser's output and anything the dashboard ever
// receives. A rejected proposal is dropped silently from the admin's
// point of view (never rendered as a broken/invalid card) but logged
// server-side with the reason, for debugging what the model attempted.
func (s *Server) validateProposals(ctx context.Context, actions []proposedAction) []proposedAction {
	if len(actions) == 0 {
		return actions
	}
	valid := make([]proposedAction, 0, len(actions))
	for _, a := range actions {
		if err := proposalValidator.Validate(ctx, s.db, a); err != nil {
			log.Printf("chatbot: dropped invalid proposal (type=%q): %v", a.Type, err)
			continue
		}
		valid = append(valid, a)
	}
	return valid
}
