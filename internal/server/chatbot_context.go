package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// workspaceContext is what the dashboard's own UI tells the assistant about
// what the Superuser is currently looking at — the admin never has to
// repeat information already visible on screen (the "Workspace Awareness"
// behavior). It's supplied by the frontend on every /api/chat request (see
// chatbotRequest.Context) and deliberately kept as a flat, easy-to-extend
// struct: wiring up a new page's context is "add a field here + a case in
// describeWorkspace," nothing more invasive than that.
//
// Only the authenticated admin chatbot (handleChatbot) ever receives a
// populated workspaceContext — the public share chat (handlePublicChat)
// always passes the zero value. That's deliberate: describeWorkspace can
// surface record contents, request logs, and provider configuration, none
// of which are safe to hand to an anonymous visitor holding a share link.
type workspaceContext struct {
	// Page is the dashboard route the admin is currently on — "collections",
	// "records", "rag", "settings", "logs", "backups", "usage", "home", or
	// "" if unknown/not sent. Drives which live-data section, if any, gets
	// appended to the prompt below.
	Page string `json:"page,omitempty"`
	// Collection is the collection name currently open — set while
	// browsing that collection's records.
	Collection string `json:"collection,omitempty"`
	// RecordID is set only while a single record is open in the record
	// editor modal (not the list view).
	RecordID string `json:"record_id,omitempty"`
}

// describeWorkspace renders the "what the admin is currently looking at"
// section of the chat prompt. It never fails the request on its own: any
// lookup error just becomes a short note in the text (e.g. "may have just
// been deleted") rather than surfacing an internal_error for what's meant
// to be a nice-to-have grounding detail, not a hard requirement — the
// assistant should still answer using the rest of the prompt if a live
// lookup can't complete.
//
// Each case injects only what's relevant to that specific page (a
// performance change): home gets a coarse collections count, the
// collections/records page gets just the one open collection's schema,
// settings gets provider configuration only, and so on — instead of the
// previous behavior of also unconditionally attaching a full listing of
// every collection and every one of its fields to every single request,
// regardless of which page (or no page at all) the admin was on. See
// answerChatbotQuestion's doc comment in chatbot_handlers.go for where
// that removed unconditional summary used to live.
func (s *Server) describeWorkspace(ctx context.Context, wc workspaceContext) string {
	if wc.Page == "" {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\nThe administrator is currently on the %q page of the dashboard.\n", wc.Page)

	switch wc.Page {
	case "home":
		// Coarse orientation only — a full per-collection field dump here
		// would just repeat what the collections/records case below
		// already sends once the admin actually opens one.
		if collections, err := listCollections(ctx, s.db); err == nil {
			fmt.Fprintf(&b, "This instance has %d collection(s).\n", len(collections))
		}

	case "collections", "records":
		if wc.Collection == "" {
			break
		}
		c, err := getCollectionByName(ctx, s.db, wc.Collection)
		if err != nil {
			fmt.Fprintf(&b, "They have collection %q open, but it couldn't be loaded (it may have just been deleted).\n", wc.Collection)
			break
		}
		fmt.Fprintf(&b, "They have collection %s open. Fields:\n", describeCollectionHeadline(c))
		b.WriteString(describeCollectionFields(c))
		// Access rules deliberately dropped from this block — not "field
		// names, field types," and the admin can ask for them directly if
		// needed.

		if wc.RecordID != "" {
			rec, err := getRecord(ctx, s.db, c, wc.RecordID)
			if err != nil {
				fmt.Fprintf(&b, "They have record %q open, but it couldn't be loaded (it may have just been deleted).\n", wc.RecordID)
			} else {
				fmt.Fprintf(&b, "The specific record open right now: %s\n", describeRecordJSON(c.Name, rec))
			}
		}

	case "rag":
		sources, err := listRAGSources(ctx, s.db, 20, "", "", "", true)
		if err != nil || len(sources) == 0 {
			b.WriteString("No RAG documents have been ingested yet.\n")
		} else {
			b.WriteString("Ingested RAG documents:\n")
			for _, src := range sources {
				line := fmt.Sprintf("  - %q: status=%s", src.Filename, src.Status)
				if src.Status == "done" {
					line += fmt.Sprintf(", %d chunk(s)", src.ChunkCount)
				}
				if src.Error != "" {
					line += ", error=" + src.Error
				}
				b.WriteString(line + "\n")
			}
		}
		fmt.Fprintf(&b, "Embedding provider configured: %v\n", s.providers.Load().embedding != nil)

	case "settings":
		bundle := s.providers.Load()
		fmt.Fprintf(&b, "Chat provider: %s, model: %s (LLM router configured: %v)\n",
			bundle.chat.Provider, orNone(bundle.chat.Model), bundle.llm != nil)
		fmt.Fprintf(&b, "Embedding provider configured: %v\n", bundle.embedding != nil)

	case "logs":
		// Recent errors only — no total-request-count framing, and no
		// need to keep scanning past the first 8 matches.
		entries, err := listLogs(ctx, s.db, 0, "")
		if err != nil {
			break
		}
		var shown int
		for _, e := range entries {
			if e.Status < 400 {
				continue
			}
			if shown == 0 {
				b.WriteString("Recent request errors:\n")
			}
			fmt.Fprintf(&b, "  - %s %s -> %d\n", e.Method, e.Path, e.Status)
			shown++
			if shown >= 8 {
				break
			}
		}
		if shown == 0 {
			b.WriteString("No recent request errors.\n")
		}
	}

	return b.String()
}

func orNone(s string) string {
	if s == "" {
		return "(none chosen)"
	}
	return s
}

// describeCollectionHeadline returns a concise, human-readable fragment
// naming a collection and its record count — %q'd name plus "(N
// record(s))" — meant to be embedded in a surrounding sentence. Shared by
// describeWorkspace's collections/records case above (the currently-open
// collection) and describeContextRefs (chatbot_context_refs.go, an
// admin's explicitly attached/@-mentioned collection reference) so both
// paths name a collection identically, not via two hand-kept-in-sync
// phrasings.
func describeCollectionHeadline(c *collection) string {
	return fmt.Sprintf("%q (%d record(s))", c.Name, c.RecordCount)
}

// describeCollectionFields renders a collection's schema as a
// human/LLM-readable bullet list, one field per line ("- name: type
// (required)" for required fields, "  (no fields defined yet)" when the
// schema is empty) — shared by describeWorkspace's collections/records
// case and describeContextRefs so a collection's schema is rendered
// identically by construction, not via two versions of the same loop kept
// in sync by hand.
func describeCollectionFields(c *collection) string {
	var b strings.Builder
	if len(c.Schema.Fields) == 0 {
		b.WriteString("  (no fields defined yet)\n")
	}
	for _, f := range c.Schema.Fields {
		extra := ""
		if f.Required {
			extra = " (required)"
		}
		if f.Type == FieldRelation {
			extra += fmt.Sprintf(" -> relates to %q (use find_related_records to traverse it)", f.RelationCollection)
		}
		fmt.Fprintf(&b, "  - %s: %s%s\n", f.Name, f.Type, extra)
	}
	return b.String()
}

// describeRecordJSON renders one record as a safe, %q-labeled JSON
// representation suitable for LLM context — labeled with its collection
// name so the fragment is self-describing wherever it's embedded. Shared
// by describeWorkspace's currently-open-record case and
// describeContextRefs's @-mentioned/dragged-in record reference.
func describeRecordJSON(collectionName string, rec map[string]any) string {
	encoded, err := json.Marshal(rec)
	if err != nil {
		return fmt.Sprintf("(failed to encode record in %q: %v)", collectionName, err)
	}
	return fmt.Sprintf("%s (in %q)", encoded, collectionName)
}

// ---------------------------------------------------------------------
// Future AI coworker architecture (advisory only today — see the
// TRUTHFULNESS, USER APPROVAL, and FUTURE AI COWORKER sections of
// chatbotSystemPrompt for the product spec this backs).
// ---------------------------------------------------------------------

// chatHistoryTurn is one prior turn of the conversation, as recorded by
// the dashboard's own chat widget — see chatbotRequest.History. Roles
// other than "user"/"assistant" are dropped by answerChatbotQuestion
// rather than forwarded to the LLM provider.
type chatHistoryTurn struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	// Attachments is a lightweight record of what (if anything) was
	// attached to this historical turn — filename/kind only, never the
	// original bytes or extracted text. See chatAttachmentRef's doc
	// comment (chatbot_attachments.go) for why full attachment data is
	// never replayed for anything but the current message.
	Attachments []chatAttachmentRef `json:"attachments,omitempty"`
}

// maxChatHistoryTurns bounds how much prior conversation gets replayed
// into the model on every request — the frontend already trims to this
// same window before sending (see app.js), this is the server-side
// backstop for any other caller of the same endpoint.
//
// 10 turns = 5 user/assistant exchanges (down from the previous 20 turns /
// 10 exchanges) — a hard truncation rather than summarizing older history
// into a paragraph, which was the other option considered: summarization
// would need its own extra model call on every request once history grows
// past the cap, adding latency and cost in the name of reducing latency
// and cost. A straight truncation has neither problem and the admin isn't
// expected to notice — greetings/pleasantries don't need it at all (see
// the fast path in answerChatbotQuestion), and real multi-turn schema
// conversations rarely run more than a handful of exchanges deep anyway.
const maxChatHistoryTurns = 10

// proposedAction is one "the assistant wants to do something concrete"
// step in the approval-based action flow described in the product spec:
// propose -> approve -> execute -> explain -> undo. Built by ActionParser
// from a model's native tool call and filtered by ProposalValidator (see
// chatbot_actions.go / chatbot_proposal_validator.go) before ever reaching
// chatbotResponse.Actions.
//
// Execution is real, split by autoExecutable (chatbot_tool_execution.go):
// safe types (create_collection, add_field, describe_onebox,
// list_collections, list_records, find_related_records) run the instant
// the model calls them, no admin action needed. Destructive types
// (delete_collection, delete_field today) still reach the admin as a
// Proposal Card first — the React AI Workspace's ProposalCard component
// (web/src/components/ai/proposal-card.tsx) then executes them for real on
// explicit confirmation, by calling the exact same REST endpoints
// (DELETE/PATCH /api/collections/...) a hand-typed request would hit, not
// a separate "approve" endpoint. rename_collection/update_schema/
// import_data are proposal-only either way — see autoExecutable's own doc
// comment for why those three have no execution primitive at all yet
// regardless of their Destructive flag. This comment previously (RC1–RC3)
// claimed no execution existed and Approve was "permanently disabled" —
// stale as of the AI-execution milestone; caught during RC4's competitive
// audit.
//
// ID is an opaque per-proposal identifier (see newActionID) for the
// frontend to key DOM nodes off of. Type is the machine-readable action
// identifier actionMeta/executeToolCall switch on (e.g. "create_collection",
// "add_field") — also the same string as the llm.Tool.Name the model
// called. Title is the short human-readable label for the proposal card's
// heading; Description is the fuller one-line summary (e.g. `Create
// collection "messages" with 1 field(s): body`). Payload is the
// type-specific structured detail (e.g. the field list) — deliberately
// `any` since it's a different concrete payload struct per Type (see
// chatbot_actions.go's payload types). Destructive flags whether this
// action needs the extra confirmation step called for by the TRUTHFULNESS
// behavior (delete collection/record/file/backup, rename, or any other
// change that loses data or can't be trivially undone).
type proposedAction struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Payload     any    `json:"payload,omitempty"`
	Destructive bool   `json:"destructive"`
	// CallIndex is the position of the llm.ToolCall this proposedAction was
	// built from within that turn's ChatResult.ToolCalls — set by
	// nativeToolCallParser, never by anything else. It exists purely for
	// the multi-round tool-execution loop (runToolLoop in
	// chatbot_handlers.go) to correlate a validated proposedAction back to
	// its exact originating ToolCall (ID and Arguments) when building the
	// tool_use/tool_result replay messages for round 2+ — see
	// chatbot_tool_execution.go. Never serialized to the frontend (a
	// backend-internal implementation detail of the loop, not something a
	// Proposal Card needs).
	CallIndex int `json:"-"`
}
