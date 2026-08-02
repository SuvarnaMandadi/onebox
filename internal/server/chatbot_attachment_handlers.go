package server

import (
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
)

// handleUploadChatAttachment is admin-only: POST /api/chat-attachments,
// backing the chat widget's drag-and-drop/paste/file-picker upload flow.
// It stores the file exactly like POST /api/files (see storeFileContent),
// tagged kind="chat_attachment" so it never shows up in the Files browser
// (same pattern as avatar uploads — see 0009_display_name_and_file_kind.sql),
// then — for document attachments only — extracts text eagerly with the
// same extractText the RAG pipeline uses (see rag_extract.go), so a chat
// request that references this attachment later never has to re-parse the
// file. A failed extraction (e.g. a scanned PDF with no text layer) still
// succeeds the upload — the admin still gets a usable attachment chip, and
// resolveAttachments (chatbot_attachments.go) tells the model the
// extraction came back empty rather than silently pretending it worked.
func (s *Server) handleUploadChatAttachment(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxUploadSize)

	if err := r.ParseMultipartForm(1 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_upload",
			"file too large or not a valid multipart/form-data upload (max "+strconv.FormatInt(s.cfg.MaxUploadSize, 10)+" bytes)", nil)
		return
	}
	defer r.MultipartForm.RemoveAll()

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing_file", `expected a "file" multipart field`, nil)
		return
	}
	defer file.Close()

	kind, ok := classifyChatAttachment(header.Filename)
	if !ok {
		writeError(w, http.StatusBadRequest, "unsupported_type",
			fmt.Sprintf("unsupported file type — supported: %s", strings.Join(chatAttachmentExtensionsList, ", ")),
			map[string]any{"supported": chatAttachmentExtensionsList})
		return
	}

	content, err := io.ReadAll(file)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_upload", "failed to read uploaded file", nil)
		return
	}

	mime := http.DetectContentType(content)
	id, err := storeFileContent(s.cfg.FilesDir, content)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to store file", nil)
		return
	}

	adminID, _ := authAdminID(r.Context())
	fileRec, err := createFileRecordKind(r.Context(), s.db, id, adminID, header.Filename, mime, int64(len(content)), "chat_attachment")
	if err != nil {
		removeStoredFile(s.cfg.FilesDir, id)
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to record file metadata", nil)
		return
	}

	var extractedText, extractionErr string
	if kind == "document" {
		if text, err := extractText(header.Filename, content); err != nil {
			extractionErr = err.Error()
		} else {
			extractedText = text
		}
	}

	if err := createChatAttachment(r.Context(), s.db, id, adminID, kind, extractedText); err != nil {
		removeStoredFile(s.cfg.FilesDir, id)
		_ = deleteFileRecord(r.Context(), s.db, id)
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to record attachment", nil)
		return
	}

	writeJSON(w, http.StatusCreated, chatAttachmentRecord{
		ID: fileRec.ID, Filename: fileRec.Filename, Mime: fileRec.Mime, Size: fileRec.Size,
		Kind: kind, Created: fileRec.Created, ExtractionError: extractionErr,
	})
}

// handleDeleteChatAttachment lets the admin remove an attachment chip
// before sending it (or clean one up after) — DELETE
// /api/chat-attachments/{id}. Mirrors handleDeleteFile's ownership check
// (fileOwnerMatches: owner or admin) even though only admins can reach
// this route at all today, so the check still holds if that ever changes.
func (s *Server) handleDeleteChatAttachment(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	rec, err := getFileByID(r.Context(), s.db, id)
	if err == sql.ErrNoRows {
		writeError(w, http.StatusNotFound, "not_found", "attachment not found", nil)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to load attachment", nil)
		return
	}
	if !fileOwnerMatches(r.Context(), rec.OwnerID) {
		writeError(w, http.StatusNotFound, "not_found", "attachment not found", nil)
		return
	}

	_ = deleteChatAttachment(r.Context(), s.db, id)
	if err := deleteFileRecord(r.Context(), s.db, id); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to delete attachment", nil)
		return
	}
	removeStoredFile(s.cfg.FilesDir, id)
	w.WriteHeader(http.StatusNoContent)
}
