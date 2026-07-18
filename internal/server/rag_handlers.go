package server

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"onebox/internal/llm"
)

func (s *Server) handleCreateRAGSource(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxUploadSize)

	if err := r.ParseMultipartForm(1 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_upload", "file too large or not a valid multipart/form-data upload", nil)
		return
	}
	defer r.MultipartForm.RemoveAll()

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing_file", `expected a "file" multipart field`, nil)
		return
	}
	defer file.Close()

	ext := strings.ToLower(filepath.Ext(header.Filename))
	if !supportedRAGExtensions[ext] {
		writeError(w, http.StatusBadRequest, "unsupported_type",
			fmt.Sprintf("unsupported file type %q — supported types: %s", ext, strings.Join(supportedRAGExtensionsList, ", ")),
			map[string]any{"received": ext, "supported": supportedRAGExtensionsList})
		return
	}

	content, err := io.ReadAll(file)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_upload", "failed to read uploaded file", nil)
		return
	}

	fileID, err := storeFileContent(s.cfg.FilesDir, content)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to store file", nil)
		return
	}

	ownerID, _ := authUserID(r.Context())
	mime := http.DetectContentType(content)
	if _, err := createFileRecord(r.Context(), s.db, fileID, ownerID, header.Filename, mime, int64(len(content))); err != nil {
		removeStoredFile(s.cfg.FilesDir, fileID)
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to record file metadata", nil)
		return
	}

	src, err := createRAGSource(r.Context(), s.db, ownerID, fileID, header.Filename)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to create rag source", nil)
		return
	}

	go ingestSource(s.db, s.providers.Load().embedding, ownerID, src, content)

	writeJSON(w, http.StatusAccepted, src)
}

// handleListRAGSources is admin-only: it powers the dashboard's RAG
// source manager.
func (s *Server) handleListRAGSources(w http.ResponseWriter, r *http.Request) {
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

	sources, err := listRAGSources(r.Context(), s.db, limit, cursorCreated, cursorID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to list rag sources", nil)
		return
	}

	hasMore := len(sources) > limit
	if hasMore {
		sources = sources[:limit]
	}
	var nextCursor string
	if hasMore && len(sources) > 0 {
		last := sources[len(sources)-1]
		nextCursor = encodeCursor(last.Created, last.ID)
	}
	if sources == nil {
		sources = []*ragSource{}
	}

	writeJSON(w, http.StatusOK, map[string]any{"items": sources, "nextCursor": nextCursor})
}

func (s *Server) handleGetRAGSource(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	src, err := getRAGSource(r.Context(), s.db, id)
	if err == sql.ErrNoRows {
		writeError(w, http.StatusNotFound, "not_found", "rag source not found", nil)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to load rag source", nil)
		return
	}
	if !fileOwnerMatches(r.Context(), src.OwnerID) {
		writeError(w, http.StatusNotFound, "not_found", "rag source not found", nil)
		return
	}
	writeJSON(w, http.StatusOK, src)
}

func (s *Server) handleDeleteRAGSource(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	src, err := getRAGSource(r.Context(), s.db, id)
	if err == sql.ErrNoRows {
		writeError(w, http.StatusNotFound, "not_found", "rag source not found", nil)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to load rag source", nil)
		return
	}
	if !fileOwnerMatches(r.Context(), src.OwnerID) {
		writeError(w, http.StatusNotFound, "not_found", "rag source not found", nil)
		return
	}
	if err := deleteRAGSource(r.Context(), s.db, s.cfg.FilesDir, src); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to delete rag source", nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type ragQueryRequest struct {
	Query string `json:"query"`
	TopK  int    `json:"top_k"`
}

type ragScoredChunk struct {
	SourceID string  `json:"source_id"`
	Text     string  `json:"text"`
	Score    float64 `json:"score"`
}

const defaultTopK = 5
const maxTopK = 20

// retrieveTopChunks embeds the query and brute-force-scores it against
// every chunk visible to the requester (see ragChunksForOwner), returning
// the top_k highest-cosine-similarity matches. Shared by /query and
// /answer.
func (s *Server) retrieveTopChunks(r *http.Request, query string, topK int) ([]ragScoredChunk, error) {
	embeddingProvider := s.providers.Load().embedding
	if embeddingProvider == nil {
		return nil, fmt.Errorf("no embedding provider configured")
	}
	if topK <= 0 {
		topK = defaultTopK
	}
	if topK > maxTopK {
		topK = maxTopK
	}

	vectors, err := embeddingProvider.Embed(r.Context(), []string{query})
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	queryVec := vectors[0]

	isAdmin := false
	if _, ok := authAdminID(r.Context()); ok {
		isAdmin = true
	}
	ownerID, _ := authUserID(r.Context())

	chunks, err := ragChunksForOwner(r.Context(), s.db, ownerID, isAdmin)
	if err != nil {
		return nil, err
	}

	scored := make([]ragScoredChunk, 0, len(chunks))
	for _, c := range chunks {
		scored = append(scored, ragScoredChunk{
			SourceID: c.SourceID,
			Text:     c.Text,
			Score:    cosineSimilarity(queryVec, c.Embedding),
		})
	}
	sort.Slice(scored, func(i, j int) bool { return scored[i].Score > scored[j].Score })
	if len(scored) > topK {
		scored = scored[:topK]
	}
	return scored, nil
}

func (s *Server) handleRAGQuery(w http.ResponseWriter, r *http.Request) {
	var req ragQueryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Query) == "" {
		writeError(w, http.StatusBadRequest, "invalid_body", "expected {\"query\": \"...\"}", nil)
		return
	}

	results, err := s.retrieveTopChunks(r, req.Query, req.TopK)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

type ragAnswerResponse struct {
	Answer  string           `json:"answer"`
	Sources []ragScoredChunk `json:"sources"`
}

func (s *Server) handleRAGAnswer(w http.ResponseWriter, r *http.Request) {
	var req ragQueryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Query) == "" {
		writeError(w, http.StatusBadRequest, "invalid_body", "expected {\"query\": \"...\"}", nil)
		return
	}
	chunks, err := s.retrieveTopChunks(r, req.Query, req.TopK)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), nil)
		return
	}
	if len(chunks) == 0 {
		writeJSON(w, http.StatusOK, ragAnswerResponse{
			Answer:  "I don't have any ingested documents to answer that from yet.",
			Sources: []ragScoredChunk{},
		})
		return
	}

	result, err := s.providers.Load().llm.Chat(r.Context(), llm.ChatRequest{
		Model: s.cfg.AnthropicModel,
		Messages: []llm.Message{
			{Role: "system", Content: ragSystemPrompt},
			{Role: "user", Content: buildRAGPrompt(req.Query, chunks)},
		},
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "LLM call failed: "+err.Error(), nil)
		return
	}
	s.logUsage(r.Context(), llm.ProviderKind(s.cfg.AnthropicModel), s.cfg.AnthropicModel, result.TokensIn, result.TokensOut, false)

	writeJSON(w, http.StatusOK, ragAnswerResponse{Answer: result.Content, Sources: chunks})
}

const ragSystemPrompt = "You are a helpful assistant. Answer the user's question using only the " +
	"provided context. If the context doesn't contain the answer, say so plainly instead of guessing."

func buildRAGPrompt(query string, chunks []ragScoredChunk) string {
	var b strings.Builder
	b.WriteString("Context:\n\n")
	for i, c := range chunks {
		fmt.Fprintf(&b, "[%d] %s\n\n", i+1, c.Text)
	}
	fmt.Fprintf(&b, "Question: %s", query)
	return b.String()
}
