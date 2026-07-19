package server

import (
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
		return t
	case bool:
		return strconv.FormatBool(t)
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return fmt.Sprintf("%v", t)
		}
		return string(b)
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

	rows, cols, err := parseImportFile(r)
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

// handleImportCollection applies a confirmed source-column -> schema-field
// mapping (form field "mapping", a JSON object) and creates one record per
// row via the same createRecord path (and validation) the regular create
// API uses. Rows that fail validation are counted, not aborted on.
func (s *Server) handleImportCollection(w http.ResponseWriter, r *http.Request) {
	uid, _ := authUserID(r.Context())
	name := chi.URLParam(r, "name")
	c, err := getCollectionByName(r.Context(), s.db, name)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "collection not found", nil)
		return
	}

	rows, _, err := parseImportFile(r)
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

	imported := 0
	var errs []string
	for i, row := range rows {
		input := row
		if mapping != nil {
			mapped := map[string]any{}
			for src, target := range mapping {
				if target == "" {
					continue
				}
				if v, ok := row[src]; ok {
					mapped[target] = v
				}
			}
			input = mapped
		}
		if _, err := createRecord(r.Context(), s.db, c, input, uid); err != nil {
			errs = append(errs, fmt.Sprintf("row %d: %v", i+1, err))
			continue
		}
		imported++
	}

	writeJSON(w, http.StatusOK, map[string]any{"imported": imported, "failed": len(errs), "errors": errs})
}

// parseImportFile reads a multipart "file" field as either JSON (an
// array of objects) or CSV (header row + data rows) based on the
// filename extension, returning parsed rows as maps plus the ordered
// column/key names (JSON: keys from the first row; CSV: the header row).
func parseImportFile(r *http.Request) ([]map[string]any, []string, error) {
	if err := r.ParseMultipartForm(20 << 20); err != nil {
		return nil, nil, fmt.Errorf("file too large or not a valid multipart/form-data upload")
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
