package server

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
)

type createCollectionRequest struct {
	Name   string `json:"name"`
	Schema Schema `json:"schema"`
	Rules  Rules  `json:"rules"`
}

func (s *Server) handleCreateCollection(w http.ResponseWriter, r *http.Request) {
	var req createCollectionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "request body must be valid JSON", nil)
		return
	}

	c, err := createCollection(r.Context(), s.db, req.Name, req.Schema, req.Rules)
	switch {
	case err == nil:
		writeJSON(w, http.StatusCreated, c)
	case err == errCollectionExists:
		writeError(w, http.StatusConflict, "collection_exists", "a collection with that name already exists", nil)
	default:
		writeError(w, http.StatusBadRequest, "invalid_collection", err.Error(), nil)
	}
}

func (s *Server) handleListCollections(w http.ResponseWriter, r *http.Request) {
	cols, err := listCollections(r.Context(), s.db)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to list collections", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": cols})
}

func (s *Server) handleGetCollection(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	c, err := getCollectionByName(r.Context(), s.db, name)
	if err == errCollectionNotFound {
		writeError(w, http.StatusNotFound, "not_found", "collection not found", nil)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to load collection", nil)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) handleDeleteCollection(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	err := deleteCollection(r.Context(), s.db, name)
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case err == errCollectionNotFound:
		writeError(w, http.StatusNotFound, "not_found", "collection not found", nil)
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to delete collection", nil)
	}
}
