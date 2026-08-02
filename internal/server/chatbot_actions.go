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

// proposedField is one entry in a create_collection action's Payload.
type proposedField struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// -- tool schemas ------------------------------------------------------
//
// Each schema is plain JSON Schema (the "object/properties/required"
// subset every provider here accepts unmodified — see llm.Tool.Schema),
// describing exactly the arguments the corresponding payload struct below
// expects. The model fills these in itself via native tool/function
// calling; nothing here is ever inferred from its prose reply.

var createCollectionSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"name": {"type": "string", "description": "The collection name (lowercase, no spaces, e.g. \"messages\")"},
		"fields": {
			"type": "array",
			"description": "The fields to create on this collection, beyond the automatic id/owner_id/created/updated columns — never list those here.",
			"items": {
				"type": "object",
				"properties": {
					"name": {"type": "string"},
					"type": {"type": "string", "enum": ["text", "number", "bool", "date", "json"]}
				},
				"required": ["name", "type"]
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
		"type": {"type": "string", "enum": ["text", "number", "bool", "date", "json"]}
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
}
type deleteCollectionPayload struct {
	Name string `json:"name"`
}
type renameCollectionPayload struct {
	From string `json:"from"`
	To   string `json:"to"`
}
type addFieldPayload struct {
	Collection string `json:"collection"`
	Field      string `json:"field"`
	Type       string `json:"type"`
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
	for _, call := range result.ToolCalls {
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
