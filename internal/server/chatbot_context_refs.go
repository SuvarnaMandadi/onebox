package server

import (
	"context"
	"fmt"
	"strings"
)

// maxContextRefs bounds how many explicit collection/record references
// one message can carry — the same kind of hard cap maxAttachmentsPerMessage
// (app.js) and maxChatHistoryTurns already apply elsewhere in this
// pipeline, here guarding against a pathological drag-drop-everything
// admin (or a buggy client) ballooning prompt size unboundedly. A real
// admin dragging in a handful of collections/records for one question
// never gets near it.
const maxContextRefs = 12

// maxAttachmentsPerMessage bounds how many attachment_ids one chat request
// may reference — the same never-trust-the-client discipline as
// maxContextRefs above, applied to a different field: the dashboard's own
// UI never lets an admin attach anywhere near this many files to one
// message, but nothing before this stopped a crafted request from listing
// hundreds of attachment_ids, each costing a DB lookup plus up to
// maxAttachmentTextChars of injected prompt text (see resolveAttachments,
// chatbot_attachments.go).
const maxAttachmentsPerMessage = 12

// maxConversationExcerptsPerMessage is maxAttachmentsPerMessage's
// counterpart for conversation_excerpts — same reasoning, applied to
// #mentioned other conversations instead of attached files (see
// describeConversationExcerpts below).
const maxConversationExcerptsPerMessage = 12

// contextRefInput is one collection/record the admin explicitly attached
// to a message — by dragging it out of the Collections/Records list, or
// picking it from an @mention — as opposed to workspaceContext, which
// only ever describes whatever page they happen to be looking at right
// now. The two are independent and both get injected: an admin on the
// Logs page can still @mention the "orders" collection without navigating
// away from what they're debugging.
//
// A "file" reference doesn't need its own entry here: the frontend adds
// it straight into AttachmentIDs instead, and resolveAttachments already
// knows how to resolve a file that was never uploaded through the
// chat-attachment endpoint (see chatAttachmentData) — so a dragged-in
// existing file and a freshly uploaded one are handled by the exact same
// code path.
type contextRefInput struct {
	// Type is "collection" or "record". Anything else is ignored rather
	// than erroring the whole request — never trust the client, same
	// principle as everywhere else in this pipeline (see
	// ProposalValidator's doc comment for the fullest statement of it).
	Type string `json:"type"`
	// Collection is the collection name — required for both types.
	Collection string `json:"collection"`
	// RecordID is set only when Type is "record".
	RecordID string `json:"record_id,omitempty"`
}

// conversationExcerptInput is a #mention reference to a DIFFERENT stored
// conversation. Unlike contextRefInput, there is nothing for the backend
// to look up: conversations live only in the dashboard's own localStorage
// (see CHAT_STORE_KEY in app.js), never in any server table, so the
// frontend has already extracted whatever excerpt it wants included, and
// this is folded into the outgoing message verbatim — the same way an
// attachment's already-extracted document text is, and for the same
// reason (the extraction happened somewhere this handler has no way to
// redo).
type conversationExcerptInput struct {
	Title string `json:"title"`
	Text  string `json:"text"`
}

// maxConversationExcerptChars bounds how much of one #mentioned
// conversation's text gets injected — same discipline as
// maxAttachmentTextChars, and for the same reason: an admin could
// reference a months-long conversation, and without a cap that alone
// would dominate the prompt.
const maxConversationExcerptChars = 4000

// describeContextRefs renders the admin's explicitly attached
// collection/record references into a text block, reusing the exact same
// schema/record-formatting describeWorkspace already uses for the
// current-page case (describeCollectionHeadline / describeCollectionFields
// / describeRecordJSON in chatbot_context.go) — an @-mentioned collection
// and the one the admin happens to be looking at are described
// identically, by construction, not by two versions of the same logic
// kept in sync by hand. A ref that fails to load (deleted mid-flight,
// malformed request) gets a short note instead of failing the request —
// same "never let a nice-to-have grounding detail block the actual
// answer" rule describeWorkspace itself follows.
func (s *Server) describeContextRefs(ctx context.Context, refs []contextRefInput) string {
	if len(refs) == 0 {
		return ""
	}
	if len(refs) > maxContextRefs {
		refs = refs[:maxContextRefs]
	}
	var b strings.Builder
	b.WriteString("\nThe administrator explicitly attached this context to their message:\n")
	for _, ref := range refs {
		switch ref.Type {
		case "collection":
			c, err := getCollectionByName(ctx, s.db, ref.Collection)
			if err != nil {
				fmt.Fprintf(&b, "- Collection %q — couldn't be loaded (it may have just been deleted).\n", ref.Collection)
				continue
			}
			fmt.Fprintf(&b, "- %s. Fields:\n%s", describeCollectionHeadline(c), describeCollectionFields(c))
		case "record":
			c, err := getCollectionByName(ctx, s.db, ref.Collection)
			if err != nil {
				fmt.Fprintf(&b, "- Record %s/%s — collection couldn't be loaded (it may have just been deleted).\n", ref.Collection, ref.RecordID)
				continue
			}
			rec, err := getRecord(ctx, s.db, c, ref.RecordID)
			if err != nil {
				fmt.Fprintf(&b, "- Record %s/%s — couldn't be loaded (it may have just been deleted).\n", ref.Collection, ref.RecordID)
				continue
			}
			fmt.Fprintf(&b, "- Record %s\n", describeRecordJSON(c.Name, rec))
		default:
			// Unrecognized Type — dropped silently rather than guessed at;
			// the client only ever sends "collection"/"record" today (see
			// app.js), so this is a forward-compatibility guard, not a
			// path real traffic hits.
		}
	}
	return b.String()
}

// describeConversationExcerpts folds #mentioned conversations' already-
// extracted text into the prompt, capping each one the same way an
// attachment's extracted document text is capped (see
// maxAttachmentTextChars / safeTruncate) — reusing safeTruncate itself so
// a multi-byte UTF-8 rune is never split at the boundary.
func describeConversationExcerpts(excerpts []conversationExcerptInput) string {
	if len(excerpts) == 0 {
		return ""
	}
	if len(excerpts) > maxConversationExcerptsPerMessage {
		excerpts = excerpts[:maxConversationExcerptsPerMessage]
	}
	var b strings.Builder
	for _, e := range excerpts {
		title := e.Title
		if title == "" {
			title = "(untitled conversation)"
		}
		text := e.Text
		truncated := false
		if len(text) > maxConversationExcerptChars {
			text = safeTruncate(text, maxConversationExcerptChars)
			truncated = true
		}
		// "(untrusted content below)" — same reasoning as the attached-
		// document delimiter (chatbot_attachments.go): this text came from a
		// PRIOR conversation, not the admin's current message, and must be
		// read as data to analyze, never as instructions to follow. See
		// chatbotSystemPrompt's UNTRUSTED CONTENT section.
		fmt.Fprintf(&b, "\n--- Referenced conversation (untrusted content below): %s ---\n", title)
		if text == "" {
			b.WriteString("(no content)\n")
		} else {
			b.WriteString(text)
			if truncated {
				b.WriteString("\n[...truncated...]")
			}
			b.WriteString("\n")
		}
	}
	return b.String()
}
