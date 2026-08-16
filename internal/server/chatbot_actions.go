package server

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"onebox/internal/llm"
)

// Action type identifiers — machine-readable operation names. Each one is
// simultaneously: (1) the llm.Tool.Name the model calls, (2) the key into
// actionMeta below, and (3) the proposedAction.Type value the frontend
// switches on (see renderActionCard in app.js) — one identifier, no
// separate mapping table to keep in sync across those three uses.
const (
	actionCreateCollection = "create_collection"
	actionDeleteCollection = "delete_collection"
	actionRenameCollection = "rename_collection"
	actionAddField         = "add_field"
	actionDeleteField      = "delete_field"
	actionImportData       = "import_data"
	actionUpdateSchema     = "update_schema"
	// actionDescribeOnebox and actionListCollections are read-only,
	// non-destructive tools with real execution backing (see
	// executeToolCall in chatbot_tool_execution.go) — added for the
	// AI-execution milestone alongside the schema-changing actions above,
	// which predate it. Unlike those, these never produce anything a
	// Proposal Card would need to show: there's nothing to approve about
	// looking something up, so autoExecutable always runs them
	// immediately.
	actionDescribeOnebox  = "describe_onebox"
	actionListCollections = "list_collections"
	// actionListRecords is the Milestone 5 addition that lets the model
	// actually see record-level data mid-conversation instead of only
	// whatever the admin happened to attach/mention up front — needed for
	// any real "detect duplicates," "find missing data," or "analyze this
	// collection" task, none of which are answerable from schema alone.
	// Read-only and safe by the same reasoning as list_collections, and
	// reuses the exact listRecords data-layer function the real
	// GET /api/collections/:name/records endpoint calls — no parallel
	// query path.
	actionListRecords = "list_records"
	// actionFindRelatedRecords is the Milestone 6 addition that lets the
	// model traverse a relation field: given one record, resolve any
	// relation field's value forward (the record it points to — "this
	// order's customer") and scan every other collection's schema for a
	// relation field pointing back at this one, reporting the matching
	// records (reverse — "every order for this customer"). Read-only,
	// reuses getCollectionByName/getRecord/listCollections/listRecords —
	// the exact same lookups real API requests already use — and is
	// autoExecutable for the same reason list_records is: nothing it does
	// can change data.
	actionFindRelatedRecords = "find_related_records"
	// actionListBackups, actionGetRecentErrors, and actionGetSettingsSummary
	// are the RC4 additions that close a real gap: every other tool up to
	// this point only ever grounds the model in collections/fields/records,
	// so a question about backup health, recent errors, or which LLM
	// provider is configured had no real data to call — the model either
	// guessed from workspaceContext (only populated when the admin happens
	// to be on that exact page — see describeWorkspace) or made something
	// up. All three are read-only and reuse the exact data-layer functions
	// their own dashboard pages already call (listBackupHistory, listLogs,
	// s.providers.Load()) — same "no parallel query path" discipline as
	// list_records/find_related_records — so they're autoExecutable for the
	// same reason those are: nothing here can change data.
	actionListBackups        = "list_backups"
	actionGetRecentErrors    = "get_recent_errors"
	actionGetSettingsSummary = "get_settings_summary"
)

// newActionID returns a short opaque identifier for one proposedAction —
// unique enough for the frontend to key DOM nodes off of today and, in
// whatever future milestone wires up an Approve endpoint, to reference in
// that request. Not a database id: nothing is ever persisted for a
// proposal that's never approved. Deliberately not the provider's own
// ToolCall.ID (see llm.ToolCall) — Ollama never sends one at all, and
// mixing "sometimes a real provider id, sometimes generated" would make
// this field's meaning depend on which provider answered, which nothing
// downstream should have to care about.
func newActionID() string {
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		return "action"
	}
	return "act_" + hex.EncodeToString(buf)
}

// proposedField is one entry in a create_collection or add_field action's
// Payload — mirrors Field (collection_schema.go) field-for-field (RC3:
// Required/RelationCollection/Validation added alongside the original
// Name/Type) so executeCreateCollection/executeAddField can convert one
// straight into the other with no lossy translation in between.
type proposedField struct {
	Name               string           `json:"name"`
	Type               string           `json:"type"`
	Required           bool             `json:"required,omitempty"`
	RelationCollection string           `json:"relation_collection,omitempty"`
	Validation         *FieldValidation `json:"validation,omitempty"`
}

// -- tool schemas ------------------------------------------------------
//
// Each schema is plain JSON Schema (the "object/properties/required"
// subset every provider here accepts unmodified — see llm.Tool.Schema),
// describing exactly the arguments the corresponding payload struct below
// expects. The model fills these in itself via native tool/function
// calling; nothing here is ever inferred from its prose reply.

// validationSchemaProp is the "validation" property shared by
// create_collection's and add_field's field-item schemas (RC3) — the same
// literal, so both tools describe validation rules identically. Rules are
// only meaningful on the matching field type — the description says so
// explicitly, since there's no JSON-Schema-level way to say "only when
// type=text" within this subset (see this file's own doc comment on why
// every schema here stays the plain object/properties/required subset).
const validationSchemaProp = `"validation": {
		"type": "object",
		"description": "Optional validation rules, only meaningful on the matching field type — never set a rule on a field type it doesn't apply to.",
		"properties": {
			"format": {"type": "string", "enum": ["email", "url"], "description": "text only"},
			"min_length": {"type": "number", "description": "text only"},
			"max_length": {"type": "number", "description": "text only"},
			"pattern": {"type": "string", "description": "text only — a regular expression the value must match"},
			"min": {"type": "number", "description": "number only"},
			"max": {"type": "number", "description": "number only"},
			"unique": {"type": "boolean", "description": "any type — no other record may share this field's value"}
		}
	}`

// fieldItemSchemaProps is create_collection's field-item shape (RC3): a
// field is no longer just name+type — it can declare required, a relation
// target, and validation rules, the same three things a real
// POST /api/collections request already accepts (see Field/FieldValidation
// in collection_schema.go).
const fieldItemSchemaProps = `{
	"name": {"type": "string"},
	"type": {"type": "string", "enum": ["text", "number", "bool", "date", "json", "relation"], "description": "Use \"relation\" for a field that references another collection's record (then set relation_collection)."},
	"required": {"type": "boolean", "description": "Whether this field must always have a value. Propose true for anything the record genuinely can't exist without (e.g. a customer's name, an order's total)."},
	"relation_collection": {"type": "string", "description": "Required when type is \"relation\" — the target collection this field points to. Must be an existing collection."},
	` + validationSchemaProp + `
}`

var createCollectionSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"name": {"type": "string", "description": "The collection name (lowercase, no spaces, e.g. \"messages\")"},
		"fields": {
			"type": "array",
			"description": "The fields to create on this collection, beyond the automatic id/owner_id/created/updated columns — never list those here.",
			"items": {
				"type": "object",
				"properties": ` + fieldItemSchemaProps + `,
				"required": ["name", "type"]
			}
		},
		"rules": {
			"type": "object",
			"description": "Who can list/view/create/update/delete records in this collection. Omit a key (or the whole object) to use the safe default (authenticated for list/view/create, owner for update/delete). Propose \"owner\" for anything personal/private, \"public\" only when the admin clearly wants unauthenticated access.",
			"properties": {
				"list": {"type": "string", "enum": ["public", "authenticated", "owner"]},
				"view": {"type": "string", "enum": ["public", "authenticated", "owner"]},
				"create": {"type": "string", "enum": ["public", "authenticated", "owner"]},
				"update": {"type": "string", "enum": ["public", "authenticated", "owner"]},
				"delete": {"type": "string", "enum": ["public", "authenticated", "owner"]}
			}
		}
	},
	"required": ["name", "fields"]
}`)

var deleteCollectionSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"name": {"type": "string", "description": "The collection to delete"}
	},
	"required": ["name"]
}`)

var renameCollectionSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"from": {"type": "string", "description": "The collection's current name"},
		"to": {"type": "string", "description": "The collection's new name"}
	},
	"required": ["from", "to"]
}`)

var addFieldSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"collection": {"type": "string"},
		"field": {"type": "string", "description": "The new field's name"},
		"type": {"type": "string", "enum": ["text", "number", "bool", "date", "json", "relation"], "description": "Use \"relation\" for a field that references another collection's record (then set relation_collection)."},
		"required": {"type": "boolean", "description": "Whether this field must always have a value."},
		"relation_collection": {"type": "string", "description": "Required when type is \"relation\" — the target collection this field points to."},
		` + validationSchemaProp + `
	},
	"required": ["collection", "field", "type"]
}`)

var deleteFieldSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"collection": {"type": "string"},
		"field": {"type": "string", "description": "The field to delete"}
	},
	"required": ["collection", "field"]
}`)

var importDataSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"collection": {"type": "string", "description": "The collection to import data into"}
	},
	"required": ["collection"]
}`)

var updateSchemaSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"collection": {"type": "string"},
		"summary": {"type": "string", "description": "A short human-readable summary of what's changing, e.g. \"add required flag to email, remove legacy_id\""}
	},
	"required": ["collection", "summary"]
}`)

// describeOneboxSchema/listCollectionsSchema take no arguments at all — an
// empty object, not omitted entirely, since every provider here expects a
// well-formed (if trivial) JSON Schema object per tool.
var describeOneboxSchema = json.RawMessage(`{"type": "object", "properties": {}}`)
var listCollectionsSchema = json.RawMessage(`{"type": "object", "properties": {}}`)

var listRecordsSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"collection": {"type": "string", "description": "The collection to read records from"},
		"limit": {"type": "number", "description": "Max records to return (default 20, capped at 50)"}
	},
	"required": ["collection"]
}`)

var findRelatedRecordsSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"collection": {"type": "string", "description": "The collection containing the starting record"},
		"record_id": {"type": "string", "description": "The id of the record to find relationships for"}
	},
	"required": ["collection", "record_id"]
}`)

// listBackupsSchema/getRecentErrorsSchema/getSettingsSummarySchema (RC4)
// take no arguments — same empty-object convention as
// describeOneboxSchema/listCollectionsSchema above.
var listBackupsSchema = json.RawMessage(`{"type": "object", "properties": {}}`)
var getRecentErrorsSchema = json.RawMessage(`{"type": "object", "properties": {}}`)
var getSettingsSummarySchema = json.RawMessage(`{"type": "object", "properties": {}}`)

// actionToolDefs is what answerChatbotQuestion offers the model on every
// full (non-greeting) chat turn — see chatbot_handlers.go. Description is
// the only channel that tells the model when to call each one (there is
// no other — see Tool's doc comment in internal/llm/llm.go), so each one
// names the concrete admin request it answers, not just its own name.
var actionToolDefs = []llm.Tool{
	{
		Name:        actionCreateCollection,
		Description: "Propose creating a new collection. Call this whenever the admin asks to create/add/set up a new collection, after you've decided on a sensible default schema — do not ask an open-ended \"what fields do you want\" question first for a common entity.",
		Schema:      createCollectionSchema,
	},
	{
		Name:        actionDeleteCollection,
		Description: "Propose deleting an existing collection. Call this whenever the admin asks to delete/remove/drop a collection.",
		Schema:      deleteCollectionSchema,
	},
	{
		Name:        actionRenameCollection,
		Description: "Propose renaming an existing collection. Call this whenever the admin asks to rename a collection.",
		Schema:      renameCollectionSchema,
	},
	{
		Name:        actionAddField,
		Description: "Propose adding a new field to an existing collection. Call this whenever the admin asks to add a field/column to a collection.",
		Schema:      addFieldSchema,
	},
	{
		Name:        actionDeleteField,
		Description: "Propose deleting a field from an existing collection. Call this whenever the admin asks to remove/delete a field from a collection.",
		Schema:      deleteFieldSchema,
	},
	{
		Name:        actionImportData,
		Description: "Propose importing data into an existing collection. Call this whenever the admin asks to import/bulk-load data into a collection.",
		Schema:      importDataSchema,
	},
	{
		Name:        actionUpdateSchema,
		Description: "Propose a broader schema change to an existing collection that isn't just adding or deleting a single field (e.g. changing several fields' types or requiredness at once). Call this for that case; use add_field/delete_field instead for a single-field change.",
		Schema:      updateSchemaSchema,
	},
	{
		Name:        actionDescribeOnebox,
		Description: "Look up a description of what OneBox is and what it does. Always call this for a general question like \"What is OneBox?\" or \"What can this do?\" instead of answering from your own general knowledge, so the answer reflects this specific product accurately.",
		Schema:      describeOneboxSchema,
	},
	{
		Name:        actionListCollections,
		Description: "List the collections that currently exist in this workspace, with their field and record counts. Call this whenever the admin asks to see/list their collections rather than guessing from workspace context alone.",
		Schema:      listCollectionsSchema,
	},
	{
		Name:        actionListRecords,
		Description: "Read actual records from a collection. Call this whenever answering requires seeing real data — finding duplicates, spotting missing/inconsistent values, summarizing or analyzing a collection's contents, or any question about specific records — rather than guessing from the schema alone. Call it more than once (different collections, or again after reasoning about the first batch) if the task needs it.",
		Schema:      listRecordsSchema,
	},
	{
		Name:        actionFindRelatedRecords,
		Description: "Follow relation fields from one record: resolves any relation field on the record to the record it points to (e.g. an order's customer_id -> the customer it belongs to), and finds every record in any OTHER collection whose relation field points back at this one (e.g. every order that references this customer). Call this whenever asked how records relate to each other — \"find this order's customer\", \"find all orders for this customer\", \"what links to this record\" — instead of guessing from field names alone.",
		Schema:      findRelatedRecordsSchema,
	},
	{
		Name:        actionListBackups,
		Description: "List this workspace's backup history (when each backup ran, its trigger, status, size, table count). Call this for any question about backups — whether one exists, how recent it is, whether backups are healthy — instead of guessing; never assume a backup exists or is recent without calling this first.",
		Schema:      listBackupsSchema,
	},
	{
		Name:        actionGetRecentErrors,
		Description: "Read the most recent failed (4xx/5xx) API requests from this instance's request log. Call this whenever asked about errors, failures, or \"what's going wrong\" rather than guessing — this is real request history, not something to infer from the schema.",
		Schema:      getRecentErrorsSchema,
	},
	{
		Name:        actionGetSettingsSummary,
		Description: "Read this instance's current configuration: which chat/embedding providers and models are set up, whether scheduled backups are configured. Call this for any question about current settings/configuration instead of guessing — never assume a provider or feature is or isn't configured without checking.",
		Schema:      getSettingsSummarySchema,
	},
}

// -- payloads ------------------------------------------------------------
//
// One struct per action type, matching its schema above field for field.
// These are proposedAction.Payload's concrete shape — json.Marshal'd with
// the same field names the frontend's renderActionCard already expects
// (see the create_collection-specific rendering there), so none of this
// milestone's frontend code needed to change.

type createCollectionPayload struct {
	Name   string          `json:"name"`
	Fields []proposedField `json:"fields"`
	// Rules is the zero value (every RuleKind "") when the model doesn't
	// propose access control — createCollection already treats that as
	// "fill in DefaultRules" for a real API request, so leaving this
	// unset here reproduces the exact same safe-by-default behavior (RC3
	// closes the gap where the model previously had no channel to propose
	// rules at all — see executeCreateCollection).
	Rules Rules `json:"rules,omitempty"`
}
type deleteCollectionPayload struct {
	Name string `json:"name"`
}
type renameCollectionPayload struct {
	From string `json:"from"`
	To   string `json:"to"`
}
type addFieldPayload struct {
	Collection         string           `json:"collection"`
	Field              string           `json:"field"`
	Type               string           `json:"type"`
	Required           bool             `json:"required,omitempty"`
	RelationCollection string           `json:"relation_collection,omitempty"`
	Validation         *FieldValidation `json:"validation,omitempty"`
}
type deleteFieldPayload struct {
	Collection string `json:"collection"`
	Field      string `json:"field"`
}
type importDataPayload struct {
	Collection string `json:"collection"`
}
type updateSchemaPayload struct {
	Collection string `json:"collection"`
	Summary    string `json:"summary"`
}
type describeOneboxPayload struct{}
type listCollectionsPayload struct{}
type listRecordsPayload struct {
	Collection string `json:"collection"`
	Limit      int    `json:"limit,omitempty"`
}
type findRelatedRecordsPayload struct {
	Collection string `json:"collection"`
	RecordID   string `json:"record_id"`
}
type listBackupsPayload struct{}
type getRecentErrorsPayload struct{}
type getSettingsSummaryPayload struct{}

// strictUnmarshal decodes a tool call's Arguments into the matching
// payload struct, rejecting any property the schema didn't declare
// (DisallowUnknownFields) rather than silently ignoring it the way a
// plain json.Unmarshal would — "reject unknown properties" is part of
// ActionParser's own structural contract, not something deferred to
// ProposalValidator, since it's still purely a shape check (does this
// JSON match the declared schema), not a domain question that needs the
// collection registry.
func strictUnmarshal(args json.RawMessage, v any) error {
	dec := json.NewDecoder(bytes.NewReader(args))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// -- action metadata -------------------------------------------------------
//
// title/destructive are fixed per action type (not inferred from
// anything the model says) — the same category of static fact as the
// actionType* constants themselves. build turns one llm.ToolCall's
// already-structured Arguments into a proposedAction: unmarshal into the
// matching payload struct, reject anything that fails to satisfy its own
// declared schema (missing required fields), and render a description
// purely from those structured fields — never from the model's prose
// reply, which is what keeps the reply and the action independent (the
// transcript can say anything; the card only ever reflects what's in
// Arguments).
type actionMetaEntry struct {
	title       string
	destructive bool
	build       func(args json.RawMessage) (description string, payload any, ok bool)
}

var actionMeta = map[string]actionMetaEntry{
	actionCreateCollection: {
		title: "Create Collection", destructive: false,
		build: func(args json.RawMessage) (string, any, bool) {
			var p createCollectionPayload
			if err := strictUnmarshal(args, &p); err != nil || p.Name == "" {
				return "", nil, false
			}
			if len(p.Fields) == 0 {
				return fmt.Sprintf("Create collection %q", p.Name), p, true
			}
			names := make([]string, len(p.Fields))
			for i, f := range p.Fields {
				names[i] = f.Name
			}
			return fmt.Sprintf("Create collection %q with %d field(s): %s", p.Name, len(p.Fields), strings.Join(names, ", ")), p, true
		},
	},
	actionDeleteCollection: {
		title: "Delete Collection", destructive: true,
		build: func(args json.RawMessage) (string, any, bool) {
			var p deleteCollectionPayload
			if err := strictUnmarshal(args, &p); err != nil || p.Name == "" {
				return "", nil, false
			}
			return fmt.Sprintf("Delete collection %q and all of its records", p.Name), p, true
		},
	},
	actionRenameCollection: {
		title: "Rename Collection", destructive: true,
		build: func(args json.RawMessage) (string, any, bool) {
			var p renameCollectionPayload
			if err := strictUnmarshal(args, &p); err != nil || p.From == "" || p.To == "" {
				return "", nil, false
			}
			return fmt.Sprintf("Rename collection %q to %q", p.From, p.To), p, true
		},
	},
	actionAddField: {
		title: "Add Field", destructive: false,
		build: func(args json.RawMessage) (string, any, bool) {
			var p addFieldPayload
			if err := strictUnmarshal(args, &p); err != nil || p.Collection == "" || p.Field == "" {
				return "", nil, false
			}
			return fmt.Sprintf("Add field %q (%s) to collection %q", p.Field, orText(p.Type), p.Collection), p, true
		},
	},
	actionDeleteField: {
		title: "Delete Field", destructive: true,
		build: func(args json.RawMessage) (string, any, bool) {
			var p deleteFieldPayload
			if err := strictUnmarshal(args, &p); err != nil || p.Collection == "" || p.Field == "" {
				return "", nil, false
			}
			return fmt.Sprintf("Delete field %q from collection %q — any data already in it is lost", p.Field, p.Collection), p, true
		},
	},
	actionImportData: {
		title: "Import Data", destructive: false,
		build: func(args json.RawMessage) (string, any, bool) {
			var p importDataPayload
			if err := strictUnmarshal(args, &p); err != nil || p.Collection == "" {
				return "", nil, false
			}
			return fmt.Sprintf("Import data into collection %q", p.Collection), p, true
		},
	},
	actionUpdateSchema: {
		title: "Update Schema", destructive: true,
		build: func(args json.RawMessage) (string, any, bool) {
			var p updateSchemaPayload
			if err := strictUnmarshal(args, &p); err != nil || p.Collection == "" {
				return "", nil, false
			}
			desc := fmt.Sprintf("Update the schema for collection %q", p.Collection)
			if p.Summary != "" {
				desc += ": " + p.Summary
			}
			return desc, p, true
		},
	},
	actionDescribeOnebox: {
		title: "Describe OneBox", destructive: false,
		build: func(args json.RawMessage) (string, any, bool) {
			var p describeOneboxPayload
			if err := strictUnmarshal(args, &p); err != nil {
				return "", nil, false
			}
			return "Look up a description of OneBox", p, true
		},
	},
	actionListCollections: {
		title: "List Collections", destructive: false,
		build: func(args json.RawMessage) (string, any, bool) {
			var p listCollectionsPayload
			if err := strictUnmarshal(args, &p); err != nil {
				return "", nil, false
			}
			return "List the workspace's collections", p, true
		},
	},
	actionListRecords: {
		title: "Read Records", destructive: false,
		build: func(args json.RawMessage) (string, any, bool) {
			var p listRecordsPayload
			if err := strictUnmarshal(args, &p); err != nil || p.Collection == "" {
				return "", nil, false
			}
			return fmt.Sprintf("Read records from collection %q", p.Collection), p, true
		},
	},
	actionFindRelatedRecords: {
		title: "Find Related Records", destructive: false,
		build: func(args json.RawMessage) (string, any, bool) {
			var p findRelatedRecordsPayload
			if err := strictUnmarshal(args, &p); err != nil || p.Collection == "" || p.RecordID == "" {
				return "", nil, false
			}
			return fmt.Sprintf("Find records related to %s/%s", p.Collection, p.RecordID), p, true
		},
	},
	actionListBackups: {
		title: "List Backups", destructive: false,
		build: func(args json.RawMessage) (string, any, bool) {
			var p listBackupsPayload
			if err := strictUnmarshal(args, &p); err != nil {
				return "", nil, false
			}
			return "List this workspace's backup history", p, true
		},
	},
	actionGetRecentErrors: {
		title: "Get Recent Errors", destructive: false,
		build: func(args json.RawMessage) (string, any, bool) {
			var p getRecentErrorsPayload
			if err := strictUnmarshal(args, &p); err != nil {
				return "", nil, false
			}
			return "Read recent failed requests from the log", p, true
		},
	},
	actionGetSettingsSummary: {
		title: "Get Settings Summary", destructive: false,
		build: func(args json.RawMessage) (string, any, bool) {
			var p getSettingsSummaryPayload
			if err := strictUnmarshal(args, &p); err != nil {
				return "", nil, false
			}
			return "Read the current provider/backup configuration", p, true
		},
	},
}

func orText(fieldType string) string {
	if fieldType == "" {
		return "text"
	}
	return fieldType
}

// -- parsing ---------------------------------------------------------------

// ActionParser turns one completed model turn into candidate
// proposedActions — the structural half of the pipeline (LLM -> ToolCall
// -> ActionParser -> ProposalValidator -> Validated Proposal -> Proposal
// Card; see ProposalValidator's doc comment in
// chatbot_proposal_validator.go for the rest of it). Its output is not
// yet safe to show the admin: it only proves the model called a known
// tool with JSON matching that tool's declared shape, never whether the
// operation is actually legal (a nonexistent collection, an already-
// taken name, ...) — that's ProposalValidator's job, run separately by
// every caller (see (*Server).validateProposals) before a proposal ever
// reaches a response.
//
// ActionParser exists as an interface, rather than answerChatbotQuestion
// calling nativeToolCallParser directly, so a temporary compatibility
// layer for a model/provider that can't do native tool calling can be
// added later (e.g. one that asks such a model to emit a fenced JSON
// block matching these same payload shapes and parses that instead)
// without touching any caller — plug in a different ActionParser,
// nothing else changes. No such layer exists today: every provider this
// codebase talks to (Anthropic, OpenAI, Ollama with a tool-capable
// model) supports native tool calling, so nativeToolCallParser is the
// only implementation, and a model that doesn't call a tool this turn
// simply proposes nothing — never a guess reconstructed from its prose.
type ActionParser interface {
	ParseActions(result llm.ChatResult) []proposedAction
}

// nativeToolCallParser maps llm.ChatResult.ToolCalls directly onto
// proposedActions — no text of any kind is inspected. Each ToolCall's
// Arguments already arrived as the provider's own decoded JSON (see
// llm.ToolCall's doc comment), so "parsing" here means exactly one
// strict JSON decode per call (strictUnmarshal — rejecting unknown
// properties as well as missing required ones), never a regex or any
// other text heuristic. Domain legality (does this collection exist, is
// this field type actually supported, ...) is deliberately out of scope
// here — see ProposalValidator.
type nativeToolCallParser struct{}

func (nativeToolCallParser) ParseActions(result llm.ChatResult) []proposedAction {
	if len(result.ToolCalls) == 0 {
		return nil
	}
	actions := make([]proposedAction, 0, len(result.ToolCalls))
	for i, call := range result.ToolCalls {
		meta, known := actionMeta[call.Name]
		if !known {
			continue // the model named a tool we never offered it — ignore rather than guess
		}
		description, payload, ok := meta.build(call.Arguments)
		if !ok {
			continue // arguments didn't satisfy the tool's own declared schema (missing/unknown/malformed) — drop rather than propose something malformed
		}
		actions = append(actions, proposedAction{
			ID: newActionID(), Type: call.Name, Title: meta.title,
			Description: description, Payload: payload, Destructive: meta.destructive,
			CallIndex: i,
		})
	}
	return actions
}

// actionParser is the single point every caller (chatbot_handlers.go)
// goes through to turn a ChatResult into candidate proposals — see
// ActionParser's doc comment for why this indirection exists and for
// where validation actually happens (it doesn't happen here). Wrapped in
// compatNormalizingParser (chatbot_actions_compat.go) — a real, live
// wire-format quirk from one local model, not a hypothetical — see that
// file's doc comment both for what it fixes and for the one-line-plus-
// file-delete removal procedure once it's no longer needed.
var actionParser ActionParser = compatNormalizingParser{inner: nativeToolCallParser{}}
