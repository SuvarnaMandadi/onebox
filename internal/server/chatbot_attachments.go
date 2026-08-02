package server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"onebox/internal/llm"
)

// maxAttachmentTextChars bounds how much of one document attachment's
// extracted text gets injected into a single chat prompt — the same
// performance discipline already applied to the system prompt (<2500
// chars), workspace context (page-scoped), and conversation history
// (maxChatHistoryTurns): an admin could easily attach a 40-page PDF, and
// without a cap that alone would blow past everything the earlier
// performance pass fought to keep small. The extracted text is stored in
// full in _chat_attachments (see createChatAttachment) — only the amount
// actually injected into a given request is capped, here.
const maxAttachmentTextChars = 6000

// chatAttachmentRef is what a historical chat turn remembers about an
// attachment that was part of it — filename and kind only, never the raw
// image bytes or extracted document text. See chatHistoryTurn.Attachments'
// doc comment (chatbot_context.go) for why: resending a full image's
// base64 (or a full document's extracted text) on every turn of every
// subsequent request, for the lifetime of the conversation, would multiply
// prompt size by conversation length for an attachment the model has
// already been shown once — the exact failure mode maxChatHistoryTurns'
// truncation already exists to prevent for plain text. describeHistoryAttachments
// turns this into a short bracketed note instead.
type chatAttachmentRef struct {
	ID       string `json:"id"`
	Filename string `json:"filename"`
	Kind     string `json:"kind"`
}

// resolvedAttachments is what resolveAttachments produces for the CURRENT
// message's attachment_ids — the only turn that ever gets full attachment
// data sent to the model (see chatAttachmentRef's doc comment for why
// historical turns don't).
type resolvedAttachments struct {
	// Images is ready to assign straight to llm.Message.Images.
	Images []llm.MessageImage
	// DocsText is every document attachment's (capped) extracted text,
	// pre-formatted and ready to append to the user message's Content.
	DocsText string
	// Refs is every attachment referenced (image and document alike),
	// for the caller to persist onto the outgoing chatHistoryTurn so a
	// later request's history replay can render the lightweight note
	// instead of nothing at all.
	Refs []chatAttachmentRef
	// Notes is a short human-readable addendum for the model — e.g.
	// explaining that N images were attached but the current chat model
	// isn't vision-capable — appended to the user message's Content
	// alongside DocsText. Empty when there's nothing to explain.
	Notes string
}

// resolveAttachments loads every attachment referenced by ids and splits
// them into image bytes (for llm.Message.Images) and document text (folded
// into the message's own Content, the same way workspace/RAG context
// already is — no provider needs special wire treatment for plain text,
// unlike images). A missing/deleted attachment ID is skipped rather than
// failing the whole chat turn — the admin's message still gets answered.
//
// visionCapable gates whether image bytes are actually attached: sending
// an image block to a model that doesn't understand one risks the
// provider rejecting the entire request over it (see llm.VisionCapable's
// doc comment), so an unsupported image is left out and reported via Notes
// instead — consistent with the TRUTHFULNESS behavior elsewhere in this
// file's sibling chatbot_handlers.go: never silently pretend something
// happened that didn't.
func (s *Server) resolveAttachments(ctx context.Context, ids []string, visionCapable bool) resolvedAttachments {
	var out resolvedAttachments
	if len(ids) == 0 {
		return out
	}

	var skippedImages int
	var docs strings.Builder
	for _, id := range ids {
		fileRec, err := getFileByID(ctx, s.db, id)
		if err != nil {
			continue
		}
		kind, extractedText, ok := s.chatAttachmentData(ctx, id, fileRec)
		if !ok {
			continue
		}
		out.Refs = append(out.Refs, chatAttachmentRef{ID: id, Filename: fileRec.Filename, Kind: kind})

		if kind == "image" {
			if !visionCapable {
				skippedImages++
				continue
			}
			data, err := os.ReadFile(filepath.Join(s.cfg.FilesDir, id))
			if err != nil {
				continue
			}
			out.Images = append(out.Images, llm.MessageImage{MediaType: fileRec.Mime, Data: data})
			continue
		}

		text := extractedText
		truncated := false
		if len(text) > maxAttachmentTextChars {
			text = safeTruncate(text, maxAttachmentTextChars)
			truncated = true
		}
		fmt.Fprintf(&docs, "\n--- Attached document: %s ---\n", fileRec.Filename)
		if text == "" {
			docs.WriteString("(no text could be extracted from this file)\n")
		} else {
			docs.WriteString(text)
			if truncated {
				docs.WriteString("\n[...truncated...]")
			}
			docs.WriteString("\n")
		}
	}

	out.DocsText = docs.String()
	if skippedImages > 0 {
		out.Notes = fmt.Sprintf(
			"\n(Note: %d attached image(s) were not sent — the current chat model doesn't support image understanding. Choose a vision-capable model in Settings to use them.)",
			skippedImages,
		)
	}
	return out
}

// chatAttachmentData returns (kind, extractedText, ok) for a referenced
// file — ok is false only when the file can't be classified at all (an
// unsupported extension slipping through, which shouldn't happen given
// client + upload-time validation, but resolveAttachments must still
// degrade gracefully rather than send the model something it never
// agreed to classify).
//
// The common case — an id from POST /api/chat-attachments — hits the
// fast path: a _chat_attachments row already has kind and, for a
// document, its text pre-extracted at upload time. M2 (AI Workspace)
// added the other case this function exists for: an admin drags an
// EXISTING file out of the Files browser (or picks it via an @File
// mention) straight into the chat composer, never through the chat-
// attachment upload endpoint at all — see AttachmentIDs' doc comment in
// chatbot_handlers.go. Such a file has no _chat_attachments row, so this
// classifies its filename and extracts document text on the fly instead,
// the exact same way handleUploadChatAttachment does at upload time —
// just deferred to first reference instead of cached in advance. Nothing
// gets written back to _chat_attachments here: a file referenced this
// way is resolved fresh on every turn it's part of, same as any other
// attachment already is on every turn of a multi-turn conversation (see
// chatAttachmentRef's doc comment for why history never replays the full
// text anyway).
func (s *Server) chatAttachmentData(ctx context.Context, id string, fileRec *fileRecord) (kind, extractedText string, ok bool) {
	if att, err := getChatAttachment(ctx, s.db, id); err == nil {
		return att.Kind, att.ExtractedText, true
	}
	kind, classified := classifyChatAttachment(fileRec.Filename)
	if !classified {
		return "", "", false
	}
	if kind != "document" {
		return kind, "", true
	}
	content, err := os.ReadFile(filepath.Join(s.cfg.FilesDir, id))
	if err != nil {
		return kind, "", true // still worth referencing; just no text this time
	}
	text, _ := extractText(fileRec.Filename, content)
	return kind, text, true
}

// safeTruncate cuts s to at most maxBytes bytes without splitting a
// multi-byte UTF-8 rune in half — extracted document text is very often
// non-ASCII (smart quotes, em dashes, any non-English language), and a
// plain byte-index slice can land mid-rune and hand the model (and the
// admin, if it's ever displayed) a broken trailing byte sequence.
func safeTruncate(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// describeHistoryAttachments renders the lightweight "an attachment was
// here" note appended to a replayed historical user turn's content — see
// chatAttachmentRef's doc comment for why this carries filenames only,
// never the original image/document data.
func describeHistoryAttachments(refs []chatAttachmentRef) string {
	if len(refs) == 0 {
		return ""
	}
	names := make([]string, len(refs))
	for i, r := range refs {
		names[i] = r.Filename
	}
	return "\n[Attached: " + strings.Join(names, ", ") + "]"
}
