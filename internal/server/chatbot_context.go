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
		fmt.Fprintf(&b, "They currently have %s open. Fields:\n%s", describeCollectionHeadline(c), describeCollectionFields(c))
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

// describeCollectionHeadline is the "collection %q (%d record(s))" label
// shared by describeWorkspace (the page the admin is currently on) and
// describeContextRefs (chatbot_context_refs.go — a collection explicitly
// attached via drag-and-drop or an @mention) — one place decides what
// identifies a collection to the model, so the two never drift apart.
func describeCollectionHeadline(c *collection) string {
	return fmt.Sprintf("collection %q (%d record(s))", c.Name, c.RecordCount)
}

// describeCollectionFields renders a collection's field list, one
// indented bullet per field — shared the same way
// describeCollectionHeadline is.
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
		fmt.Fprintf(&b, "  - %s: %s%s\n", f.Name, f.Type, extra)
	}
	return b.String()
}

// describeRecordJSON renders one record as compact JSON, labeled with its
// collection — shared by describeWorkspace and describeContextRefs.
func describeRecordJSON(collectionName string, rec map[string]any) string {
	encoded, err := json.Marshal(rec)
	if err != nil {
		return fmt.Sprintf("(record in %q couldn't be encoded)", collectionName)
	}
	return fmt.Sprintf("(in %q) %s", collectionName, encoded)
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

// proposedAction is the structured, machine-renderable shape of a single
// "the assistant wants to do something concrete" step — user-facing term
// is "Proposal" (never "Action"; see renderActionCard's doc comment in
// app.js for why), rendered by the dashboard as a Proposal Card below the
// chat message instead of the admin having to parse it out of free text.
// It's one step in the eventual approval flow: propose -> validate ->
// approve -> execute -> explain -> undo. Only "propose" and "validate"
// exist today — no code path executes a proposedAction, and its approval
// control is permanently disabled client-side ("Waiting for approval").
//
// Every proposedAction comes from the model explicitly calling one of
// actionToolDefs (native tool/function calling — see internal/llm.Tool),
// nativeToolCallParser turning that already-structured llm.ToolCall into
// this shape (chatbot_actions.go), and — before it ever reaches a
// response — proposalValidator checking it against OneBox's real schema
// engine and current collection state (chatbot_proposal_validator.go).
// Nothing here is ever inferred by parsing the assistant's natural-
// language reply: the reply and the proposal are two independent parts
// of the same ChatResult, and a model that calls a tool with no
// accompanying text (or vice versa) is expected, not a bug. This is
// deliberate architecture, not an implementation detail: it's what lets
// a future execution milestone wire an Approve control straight to
// Type+Payload with no NLU step in between, and what lets any future
// provider join in just by implementing the same llm.Tool contract.
//
// ID is a short opaque identifier unique within one response (see
// newActionID) — stable enough for the frontend to key off of and, once
// execution exists, to send back on Approve. Type is both the llm.Tool
// name the model called and a future executor's switch key (see the
// actionType* constants in chatbot_actions.go — e.g. "create_collection",
// "add_field"). Title is the short label a Proposal Card's header shows
// (e.g. "Create Collection"). Description is a human-readable one-line
// summary built purely from Payload's own fields (e.g. `Create collection
// "messages" with 4 field(s)`) — never from the reply text. Payload is
// the Type-specific structured detail the model provided as that tool's
// arguments (e.g. the field list) — deliberately `any` since no executor
// exists yet to constrain its shape at this layer; each type's concrete
// payload struct lives next to its schema in chatbot_actions.go.
// Destructive flags whether this action would need the extra confirmation
// step called for by the TRUTHFULNESS behavior (delete collection/field,
// rename, schema change) once execution exists — a fixed fact per Type,
// not something the model reports.
type proposedAction struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Payload     any    `json:"payload,omitempty"`
	Destructive bool   `json:"destructive"`
}
