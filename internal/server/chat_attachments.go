package server

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
)

// chatAttachmentExtensions is the allowlist for POST /api/chat-attachments —
// exactly the file types the multimodal chat feature was asked to support:
// images sent to vision-capable providers, documents made available as
// chat context. Deliberately a separate allowlist from the RAG ingestion
// pipeline's supportedRAGExtensions (see rag_extract.go) — these are two
// different upload surfaces with different supported-type requirements,
// and widening one must never silently widen the other.
var chatAttachmentExtensions = map[string]string{ // ext -> "image" | "document"
	".png":  "image",
	".jpg":  "image",
	".jpeg": "image",
	".pdf":  "document",
	".docx": "document",
	".txt":  "document",
	".md":   "document",
	".csv":  "document",
	".xlsx": "document",
}

// chatAttachmentExtensionsList is chatAttachmentExtensions in a stable,
// human-readable order, for error messages.
var chatAttachmentExtensionsList = []string{".png", ".jpg", ".jpeg", ".pdf", ".docx", ".txt", ".md", ".csv", ".xlsx"}

// classifyChatAttachment reports whether filename's extension is supported
// and, if so, whether it's an "image" (sent to the provider as a vision
// attachment) or a "document" (text-extracted and injected as chat
// context) — see resolveAttachments in chatbot_attachments.go for what
// each kind actually does with the file.
func classifyChatAttachment(filename string) (kind string, ok bool) {
	ext := strings.ToLower(filepath.Ext(filename))
	kind, ok = chatAttachmentExtensions[ext]
	return kind, ok
}

// chatAttachmentRecord is the response shape for POST /api/chat-attachments
// — enough for the frontend to render an attachment chip and remember the
// ID to send with the next chat message. It deliberately never includes
// the raw file bytes or the full extracted document text (see
// maxAttachmentTextChars in chatbot_attachments.go for why that stays
// server-side, injected only when the attachment is actually referenced by
// a chat request) — an image preview is rendered client-side from the
// File object the admin picked, before the upload even completes.
type chatAttachmentRecord struct {
	ID              string `json:"id"`
	Filename        string `json:"filename"`
	Mime            string `json:"mime"`
	Size            int64  `json:"size"`
	Kind            string `json:"kind"` // "image" or "document"
	Created         string `json:"created"`
	ExtractionError string `json:"extraction_error,omitempty"`
}

func createChatAttachment(ctx context.Context, sqlDB *sql.DB, id, ownerID, kind, extractedText string) error {
	_, err := sqlDB.ExecContext(ctx,
		`INSERT INTO _chat_attachments (id, owner_id, kind, extracted_text) VALUES (?, ?, ?, ?)`,
		id, nullableString(ownerID), kind, extractedText,
	)
	if err != nil {
		return fmt.Errorf("insert chat attachment: %w", err)
	}
	return nil
}

// chatAttachmentRow is a _chat_attachments row as loaded back for message
// resolution (see resolveAttachments) — narrower than chatAttachmentRecord
// since callers here always already have the matching fileRecord (filename/
// mime/size) from getFileByID and only need the chat-specific columns.
type chatAttachmentRow struct {
	ID            string
	Kind          string
	ExtractedText string
}

func getChatAttachment(ctx context.Context, sqlDB *sql.DB, id string) (*chatAttachmentRow, error) {
	row := sqlDB.QueryRowContext(ctx, `SELECT id, kind, extracted_text FROM _chat_attachments WHERE id = ?`, id)
	var a chatAttachmentRow
	if err := row.Scan(&a.ID, &a.Kind, &a.ExtractedText); err != nil {
		return nil, err
	}
	return &a, nil
}

func deleteChatAttachment(ctx context.Context, sqlDB *sql.DB, id string) error {
	_, err := sqlDB.ExecContext(ctx, `DELETE FROM _chat_attachments WHERE id = ?`, id)
	return err
}
