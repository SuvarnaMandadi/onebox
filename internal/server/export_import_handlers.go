package server

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
)

// exportCollectionRecords pages through every record in a collection via
// the existing listRecords helper (same columns/JSON conversions the
// regular list API uses) rather than a separate raw query.
func exportCollectionRecords(r *http.Request, s *Server, c *collection) ([]map[string]any, error) {
	var out []map[string]any
	cursorTime, cursorID := "", ""
	for {
		params := recordListParams{limit: maxLimit, descending: false, cursorTime: cursorTime, cursorID: cursorID}
		page, err := listRecords(r.Context(), s.db, c, params)
		if err != nil {
			return nil, err
		}
		hasMore := len(page) > params.limit
		if hasMore {
			page = page[:params.limit]
		}
		out = append(out, page...)
		if !hasMore || len(page) == 0 {
			break
		}
		last := page[len(page)-1]
		cursorTime, _ = last["created"].(string)
		cursorID, _ = last["id"].(string)
	}
	return out, nil
}

func (s *Server) handleExportCollection(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	c, err := getCollectionByName(r.Context(), s.db, name)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "collection not found", nil)
		return
	}

	records, err := exportCollectionRecords(r, s, c)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to export records", nil)
		return
	}

	if r.URL.Query().Get("format") == "csv" {
		writeCSVExport(w, name, c, records)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`.json"`)
	json.NewEncoder(w).Encode(records)
}

func writeCSVExport(w http.ResponseWriter, name string, c *collection, records []map[string]any) {
	cols := []string{"id", "owner_id", "created", "updated"}
	for _, f := range c.Schema.Fields {
		cols = append(cols, f.Name)
	}

	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`.csv"`)
	cw := csv.NewWriter(w)
	defer cw.Flush()
	cw.Write(cols)
	for _, rec := range records {
		row := make([]string, len(cols))
		for i, col := range cols {
			row[i] = csvCell(rec[col])
		}
		cw.Write(row)
	}
}

func csvCell(v any) string {
	if v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return escapeCSVFormula(t)
	case bool:
		return strconv.FormatBool(t)
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return escapeCSVFormula(fmt.Sprintf("%v", t))
		}
		return escapeCSVFormula(string(b))
	}
}

// escapeCSVFormula guards against CSV formula injection: Excel/Sheets
// treats a cell whose value starts with =, +, -, or @ as a formula to
// evaluate the moment the exported file is opened, not literal text — so a
// record field containing e.g. `=HYPERLINK("http://evil","click")` would
// silently turn into a live, clickable formula for whoever opens the
// export. Prefixing a single leading quote is the standard mitigation:
// every spreadsheet application treats a leading `'` as "the rest of this
// cell is literal text," and it's invisible in the rendered cell (Excel/
// Sheets both strip it from display). Only applies when the value already
// starts with one of the four trigger characters — every other value is
// returned completely untouched.
func escapeCSVFormula(s string) string {
	if s == "" {
		return s
	}
	switch s[0] {
	case '=', '+', '-', '@':
		return "'" + s
	default:
		return s
	}
}

type importPreviewResponse struct {
	Columns      []string          `json:"columns"`
	SuggestedMap map[string]string `json:"suggested_map"` // source column -> schema field name ("" = unmapped)
	SampleRows   []map[string]any  `json:"sample_rows"`
	TotalRows    int               `json:"total_rows"`
	SchemaFields []string          `json:"schema_fields"`
}

// handleImportPreview parses the uploaded file's shape (JSON array of
// objects, or CSV header) without writing anything, so the dashboard can
// show a field-mapping table for the admin to confirm/adjust before the
// real import.
func (s *Server) handleImportPreview(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	c, err := getCollectionByName(r.Context(), s.db, name)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "collection not found", nil)
		return
	}

	rows, cols, err := parseImportFile(w, r, s.cfg.MaxUploadSize)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_upload", err.Error(), nil)
		return
	}

	fieldByLower := make(map[string]string, len(c.Schema.Fields))
	schemaFields := make([]string, len(c.Schema.Fields))
	for i, f := range c.Schema.Fields {
		schemaFields[i] = f.Name
		fieldByLower[strings.ToLower(f.Name)] = f.Name
	}

	suggested := map[string]string{}
	for _, col := range cols {
		suggested[col] = fieldByLower[strings.ToLower(strings.TrimSpace(col))]
	}

	sample := rows
	if len(sample) > 5 {
		sample = sample[:5]
	}

	writeJSON(w, http.StatusOK, importPreviewResponse{
		Columns:      cols,
		SuggestedMap: suggested,
		SampleRows:   sample,
		TotalRows:    len(rows),
		SchemaFields: schemaFields,
	})
}

// applyImportMapping turns one raw source row into schema-field input,
// following the confirmed column -> field mapping — factored out of
// handleImportCollection so both its validation pass (atomic mode) and
// its write pass build input identically.
func applyImportMapping(row map[string]any, mapping map[string]string) map[string]any {
	if mapping == nil {
		return row
	}
	mapped := map[string]any{}
	for src, target := range mapping {
		if target == "" {
			continue
		}
		if v, ok := row[src]; ok {
			mapped[target] = v
		}
	}
	return mapped
}

// findRecordIDByField looks up a single record by an exact field match —
// the conflict-detection lookup RC3's duplicate handling needs — via the
// same recordListParams.filters mechanism GET .../records?filter= already
// exposes over HTTP, not a new query path. Returns ("", false) when no
// match exists (not an error — "no conflict" is the common, expected
// case).
func findRecordIDByField(ctx context.Context, sqlDB *sql.DB, c *collection, field string, value any) (string, bool) {
	if value == nil {
		return "", false
	}
	recs, err := listRecords(ctx, sqlDB, c, recordListParams{
		filters: map[string]string{field: fmt.Sprint(value)},
		limit:   1,
	})
	if err != nil || len(recs) == 0 {
		return "", false
	}
	id, _ := recs[0]["id"].(string)
	return id, id != ""
}

// handleImportCollection applies a confirmed source-column -> schema-field
// mapping (form field "mapping", a JSON object) and creates one record per
// row via the same createRecord path (and validation) the regular create
// API uses.
//
// Two RC3 additions, both optional (omitting them reproduces the exact
// original behavior — every row always creates a new record, partial
// failures are counted, not aborted on):
//
//   - conflict_field (+ conflict_strategy, "skip" or "replace"): before
//     creating a row, look up whether a record already has this field's
//     value. "skip" leaves the existing record alone and counts the row as
//     skipped; "replace" updates it via the same updateRecord path
//     PATCH .../records/:id uses. The lookup runs against live data on
//     every row, sequentially, so two rows in the same import file that
//     share a key are themselves caught as a conflict, not just rows
//     against pre-existing data.
//   - atomic (bool): when true, every row is validated (required/type/
//     relation/unique — the same checks a real POST would run) before
//     anything is written; if any row fails, the import stops with a
//     complete list of every failure and creates nothing at all, instead
//     of leaving a partial import behind. Row-level conflict handling
//     still runs during the write pass — validation and "does this
//     conflict with an existing record" are different questions.
func (s *Server) handleImportCollection(w http.ResponseWriter, r *http.Request) {
	uid, _ := authUserID(r.Context())
	name := chi.URLParam(r, "name")
	c, err := getCollectionByName(r.Context(), s.db, name)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "collection not found", nil)
		return
	}

	rows, _, err := parseImportFile(w, r, s.cfg.MaxUploadSize)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_upload", err.Error(), nil)
		return
	}

	var mapping map[string]string
	if raw := r.FormValue("mapping"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &mapping); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_body", "mapping must be a JSON object", nil)
			return
		}
	}

	conflictField := r.FormValue("conflict_field")
	conflictStrategy := r.FormValue("conflict_strategy")
	if conflictField != "" && conflictStrategy != "skip" && conflictStrategy != "replace" {
		writeError(w, http.StatusBadRequest, "invalid_body", `conflict_strategy must be "skip" or "replace" when conflict_field is set`, nil)
		return
	}
	atomic := r.FormValue("atomic") == "true"

	if atomic {
		var failures []string
		for i, row := range rows {
			input := applyImportMapping(row, mapping)
			if err := validateRecordInput(input, c.Schema, true); err != nil {
				failures = append(failures, fmt.Sprintf("row %d: %v", i+1, err))
				continue
			}
			if err := validateRelationValues(r.Context(), s.db, c, input); err != nil {
				failures = append(failures, fmt.Sprintf("row %d: %v", i+1, err))
			}
		}
		if len(failures) > 0 {
			writeError(w, http.StatusBadRequest, "import_validation_failed",
				fmt.Sprintf("atomic import: %d row(s) failed validation — nothing was imported", len(failures)),
				map[string]any{"errors": failures})
			return
		}
	}

	imported, updated, skipped := 0, 0, 0
	var errs []string
	for i, row := range rows {
		input := applyImportMapping(row, mapping)

		if conflictField != "" {
			if existingID, found := findRecordIDByField(r.Context(), s.db, c, conflictField, input[conflictField]); found {
				if conflictStrategy == "skip" {
					skipped++
					continue
				}
				// "replace"
				if _, err := updateRecord(r.Context(), s.db, c, existingID, input); err != nil {
					errs = append(errs, fmt.Sprintf("row %d: %v", i+1, err))
					continue
				}
				updated++
				continue
			}
		}

		if _, err := createRecord(r.Context(), s.db, c, input, uid); err != nil {
			errs = append(errs, fmt.Sprintf("row %d: %v", i+1, err))
			continue
		}
		imported++
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"imported": imported, "updated": updated, "skipped": skipped, "failed": len(errs), "errors": errs,
	})
}

// parseImportFile reads a multipart "file" field as either JSON (an
// array of objects) or CSV (header row + data rows) based on the
// filename extension, returning parsed rows as maps plus the ordered
// column/key names (JSON: keys from the first row; CSV: the header row).
//
// r.Body is wrapped in http.MaxBytesReader before ParseMultipartForm ever
// touches it — the same pattern every other upload handler in this
// codebase already applies (handleUploadFile in file_handlers.go,
// handleUploadChatAttachment, the RAG/backup upload handlers) — since
// ParseMultipartForm on its own has no size limit of its own to fall back
// on; without this, an import request could read an unbounded body into
// memory before the 20<<20 in-memory-part threshold below even applies.
func parseImportFile(w http.ResponseWriter, r *http.Request, maxUploadSize int64) ([]map[string]any, []string, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)
	if err := r.ParseMultipartForm(20 << 20); err != nil {
		return nil, nil, fmt.Errorf("file too large or not a valid multipart/form-data upload (max %d bytes)", maxUploadSize)
	}
	defer r.MultipartForm.RemoveAll()

	file, header, err := r.FormFile("file")
	if err != nil {
		return nil, nil, fmt.Errorf(`expected a "file" multipart field`)
	}
	defer file.Close()

	content, err := io.ReadAll(file)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read uploaded file")
	}

	if strings.HasSuffix(strings.ToLower(header.Filename), ".csv") {
		return parseCSVImport(content)
	}
	return parseJSONImport(content)
}

func parseJSONImport(content []byte) ([]map[string]any, []string, error) {
	var rows []map[string]any
	if err := json.Unmarshal(content, &rows); err != nil {
		return nil, nil, fmt.Errorf("file is not a JSON array of objects: %w", err)
	}
	colSet := map[string]bool{}
	var cols []string
	for _, row := range rows {
		for k := range row {
			if !colSet[k] {
				colSet[k] = true
				cols = append(cols, k)
			}
		}
	}
	return rows, cols, nil
}

func parseCSVImport(content []byte) ([]map[string]any, []string, error) {
	reader := csv.NewReader(strings.NewReader(string(content)))
	reader.FieldsPerRecord = -1
	records, err := reader.ReadAll()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse CSV: %w", err)
	}
	if len(records) == 0 {
		return nil, nil, nil
	}
	header := records[0]
	rows := make([]map[string]any, 0, len(records)-1)
	for _, rec := range records[1:] {
		row := map[string]any{}
		for i, col := range header {
			if i < len(rec) {
				row[col] = rec[i]
			}
		}
		rows = append(rows, row)
	}
	return rows, header, nil
}
