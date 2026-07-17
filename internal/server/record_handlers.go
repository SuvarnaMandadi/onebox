package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
)

type collCtxKey string

const ctxKeyCollection collCtxKey = "collection"

var (
	errInvalidLimit  = errors.New("limit must be a positive integer")
	errInvalidSort   = errors.New(`sort must be "created" or "-created"`)
	errInvalidCursor = errors.New("cursor is malformed")
	errInvalidFilter = errors.New("filter must be a comma-separated list of field=value pairs on known fields")
)

// loadCollection resolves the {name} URL param to a registered collection
// and stores it in the request context, 404-ing if it doesn't exist.
func (s *Server) loadCollection(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		ctx := context.WithValue(r.Context(), ctxKeyCollection, c)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func collectionFromContext(ctx context.Context) *collection {
	c, _ := ctx.Value(ctxKeyCollection).(*collection)
	return c
}

func (s *Server) handleListRecords(w http.ResponseWriter, r *http.Request) {
	c := collectionFromContext(r.Context())

	params, err := parseListParams(r, c.Schema)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_query", err.Error(), nil)
		return
	}

	if c.Rules.List == RuleOwner {
		uid, ok := authUserID(r.Context())
		if !ok {
			if _, isAdmin := authAdminID(r.Context()); !isAdmin {
				writeError(w, http.StatusForbidden, "forbidden", "authentication required", nil)
				return
			}
		} else {
			params.filters["owner_id"] = uid
		}
	} else if !ruleAllows(r.Context(), c.Rules.List) {
		writeError(w, http.StatusForbidden, "forbidden", "not allowed to list this collection", nil)
		return
	}

	recs, err := listRecords(r.Context(), s.db, c, params)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to list records", nil)
		return
	}

	hasMore := len(recs) > params.limit
	if hasMore {
		recs = recs[:params.limit]
	}

	var nextCursor string
	if hasMore && len(recs) > 0 {
		last := recs[len(recs)-1]
		nextCursor = encodeCursor(last["created"].(string), last["id"].(string))
	}

	if recs == nil {
		recs = []map[string]any{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":      recs,
		"nextCursor": nextCursor,
	})
}

func parseListParams(r *http.Request, schema Schema) (recordListParams, error) {
	q := r.URL.Query()

	limit := defaultLimit
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			return recordListParams{}, errInvalidLimit
		}
		limit = n
	}
	if limit > maxLimit {
		limit = maxLimit
	}

	descending := true
	if sort := q.Get("sort"); sort != "" {
		switch sort {
		case "created":
			descending = false
		case "-created":
			descending = true
		default:
			return recordListParams{}, errInvalidSort
		}
	}

	params := recordListParams{
		filters:    map[string]string{},
		descending: descending,
		limit:      limit,
	}

	if cursor := q.Get("cursor"); cursor != "" {
		created, id, ok := decodeCursor(cursor)
		if !ok {
			return recordListParams{}, errInvalidCursor
		}
		params.cursorTime, params.cursorID = created, id
	}

	if filter := q.Get("filter"); filter != "" {
		allowed := map[string]bool{"owner_id": true}
		for _, f := range schema.Fields {
			allowed[f.Name] = true
		}
		for _, clause := range strings.Split(filter, ",") {
			field, val, ok := strings.Cut(clause, "=")
			if !ok || field == "" {
				return recordListParams{}, errInvalidFilter
			}
			if !allowed[field] {
				return recordListParams{}, errInvalidFilter
			}
			params.filters[field] = val
		}
	}

	return params, nil
}

func (s *Server) handleCreateRecord(w http.ResponseWriter, r *http.Request) {
	c := collectionFromContext(r.Context())

	if !ruleAllows(r.Context(), c.Rules.Create) {
		writeError(w, http.StatusForbidden, "forbidden", "not allowed to create records in this collection", nil)
		return
	}

	var input map[string]any
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "request body must be valid JSON", nil)
		return
	}
	if err := validateRecordInput(input, c.Schema, true); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_record", err.Error(), nil)
		return
	}

	ownerID, _ := authUserID(r.Context())

	rec, err := createRecord(r.Context(), s.db, c, input, ownerID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to create record", nil)
		return
	}
	s.hub.publish(realtimeEvent{Action: "create", Collection: c.Name, Record: rec}, c.Rules.View, ownerIDOf(rec))
	writeJSON(w, http.StatusCreated, rec)
}

func (s *Server) handleGetRecord(w http.ResponseWriter, r *http.Request) {
	c := collectionFromContext(r.Context())
	id := chi.URLParam(r, "id")

	rec, err := getRecord(r.Context(), s.db, c, id)
	if err == sql.ErrNoRows {
		writeError(w, http.StatusNotFound, "not_found", "record not found", nil)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to load record", nil)
		return
	}

	if !ruleAllowsRecord(r.Context(), c.Rules.View, rec["owner_id"]) {
		writeError(w, http.StatusNotFound, "not_found", "record not found", nil)
		return
	}

	writeJSON(w, http.StatusOK, rec)
}

func (s *Server) handleUpdateRecord(w http.ResponseWriter, r *http.Request) {
	c := collectionFromContext(r.Context())
	id := chi.URLParam(r, "id")

	existing, err := getRecord(r.Context(), s.db, c, id)
	if err == sql.ErrNoRows {
		writeError(w, http.StatusNotFound, "not_found", "record not found", nil)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to load record", nil)
		return
	}
	if !ruleAllowsRecord(r.Context(), c.Rules.Update, existing["owner_id"]) {
		writeError(w, http.StatusNotFound, "not_found", "record not found", nil)
		return
	}

	var input map[string]any
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "request body must be valid JSON", nil)
		return
	}
	if err := validateRecordInput(input, c.Schema, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_record", err.Error(), nil)
		return
	}

	rec, err := updateRecord(r.Context(), s.db, c, id, input)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to update record", nil)
		return
	}
	s.hub.publish(realtimeEvent{Action: "update", Collection: c.Name, Record: rec}, c.Rules.View, ownerIDOf(rec))
	writeJSON(w, http.StatusOK, rec)
}

func (s *Server) handleDeleteRecord(w http.ResponseWriter, r *http.Request) {
	c := collectionFromContext(r.Context())
	id := chi.URLParam(r, "id")

	existing, err := getRecord(r.Context(), s.db, c, id)
	if err == sql.ErrNoRows {
		writeError(w, http.StatusNotFound, "not_found", "record not found", nil)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to load record", nil)
		return
	}
	if !ruleAllowsRecord(r.Context(), c.Rules.Delete, existing["owner_id"]) {
		writeError(w, http.StatusNotFound, "not_found", "record not found", nil)
		return
	}

	if err := deleteRecord(r.Context(), s.db, c, id); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to delete record", nil)
		return
	}
	s.hub.publish(realtimeEvent{Action: "delete", Collection: c.Name, Record: existing}, c.Rules.View, ownerIDOf(existing))
	w.WriteHeader(http.StatusNoContent)
}
