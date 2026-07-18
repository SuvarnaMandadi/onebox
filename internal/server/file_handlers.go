package server

import (
	"database/sql"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
)

func parseCreatedTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// handleUploadFile accepts a multipart/form-data upload with a single
// "file" part. The caller must be an authenticated user or admin; the
// uploaded file's owner_id is set to the uploading user (empty for
// admin-only uploads).
func (s *Server) handleUploadFile(w http.ResponseWriter, r *http.Request) {
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

	ownerID, _ := authUserID(r.Context())

	rec, err := createFileRecord(r.Context(), s.db, id, ownerID, header.Filename, mime, int64(len(content)))
	if err != nil {
		_ = os.Remove(filepath.Join(s.cfg.FilesDir, id))
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to record file metadata", nil)
		return
	}

	writeJSON(w, http.StatusCreated, rec)
}

// handleListFiles powers both the admin dashboard's file browser (sees
// every file) and a regular user's Home page (sees only files they
// uploaded) — the same owner-vs-admin scoping already used for RAG chunks.
func (s *Server) handleListFiles(w http.ResponseWriter, r *http.Request) {
	limit := defaultLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > maxLimit {
		limit = maxLimit
	}

	var cursorCreated, cursorID string
	if cursor := r.URL.Query().Get("cursor"); cursor != "" {
		created, id, ok := decodeCursor(cursor)
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid_query", "cursor is malformed", nil)
			return
		}
		cursorCreated, cursorID = created, id
	}

	_, isAdmin := authAdminID(r.Context())
	ownerID, _ := authUserID(r.Context())

	files, err := listFiles(r.Context(), s.db, limit, cursorCreated, cursorID, ownerID, isAdmin)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to list files", nil)
		return
	}

	hasMore := len(files) > limit
	if hasMore {
		files = files[:limit]
	}
	var nextCursor string
	if hasMore && len(files) > 0 {
		last := files[len(files)-1]
		nextCursor = encodeCursor(last.Created, last.ID)
	}
	if files == nil {
		files = []*fileRecord{}
	}

	total, err := countFiles(r.Context(), s.db, ownerID, isAdmin)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to count files", nil)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"items": files, "nextCursor": nextCursor, "total": total})
}

func (s *Server) handleServeFile(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	rec, err := getFileByID(r.Context(), s.db, id)
	if err == sql.ErrNoRows {
		writeError(w, http.StatusNotFound, "not_found", "file not found", nil)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to load file", nil)
		return
	}
	if !fileOwnerMatches(r.Context(), rec.OwnerID) {
		writeError(w, http.StatusNotFound, "not_found", "file not found", nil)
		return
	}

	path := filepath.Join(s.cfg.FilesDir, rec.ID)
	f, err := os.Open(path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to open file", nil)
		return
	}
	defer f.Close()

	w.Header().Set("Content-Type", rec.Mime)
	http.ServeContent(w, r, rec.Filename, parseCreatedTime(rec.Created), f)
}

func (s *Server) handleDeleteFile(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	rec, err := getFileByID(r.Context(), s.db, id)
	if err == sql.ErrNoRows {
		writeError(w, http.StatusNotFound, "not_found", "file not found", nil)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to load file", nil)
		return
	}
	if !fileOwnerMatches(r.Context(), rec.OwnerID) {
		writeError(w, http.StatusNotFound, "not_found", "file not found", nil)
		return
	}

	if err := deleteFileRecord(r.Context(), s.db, id); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to delete file record", nil)
		return
	}
	removeStoredFile(s.cfg.FilesDir, rec.ID)

	w.WriteHeader(http.StatusNoContent)
}
