package server

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"onebox/internal/config"
)

func createTestCollection(t *testing.T, srv *Server, adminToken, name string) {
	t.Helper()
	rec := doAuth(t, srv, http.MethodPost, "/api/collections", adminToken, map[string]any{
		"name":   name,
		"schema": Schema{Fields: []Field{{Name: "title", Type: FieldText, Required: true}}},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create collection: status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestExportAndImportCollectionJSON(t *testing.T) {
	srv, _ := newTestServer(t)
	adminToken := bootstrapAdmin(t, srv)
	createTestCollection(t, srv, adminToken, "notes")

	doAuth(t, srv, http.MethodPost, "/api/collections/notes/records", adminToken, map[string]any{"title": "first"})
	doAuth(t, srv, http.MethodPost, "/api/collections/notes/records", adminToken, map[string]any{"title": "second"})

	exportRec := doAuth(t, srv, http.MethodGet, "/api/collections/notes/export?format=json", adminToken, nil)
	if exportRec.Code != http.StatusOK {
		t.Fatalf("export: status = %d, body = %s", exportRec.Code, exportRec.Body.String())
	}
	var exported []map[string]any
	if err := json.Unmarshal(exportRec.Body.Bytes(), &exported); err != nil {
		t.Fatalf("decode export: %v", err)
	}
	if len(exported) != 2 {
		t.Fatalf("exported %d records, want 2", len(exported))
	}

	createTestCollection(t, srv, adminToken, "notes2")

	previewReq := multipartUploadRequest(t, "/api/collections/notes2/import/preview", "file", "notes.json", exportRec.Body.Bytes())
	previewReq.Header.Set("Authorization", "Bearer "+adminToken)
	previewRec := httptest.NewRecorder()
	srv.Router().ServeHTTP(previewRec, previewReq)
	if previewRec.Code != http.StatusOK {
		t.Fatalf("preview: status = %d, body = %s", previewRec.Code, previewRec.Body.String())
	}
	var preview importPreviewResponse
	json.Unmarshal(previewRec.Body.Bytes(), &preview)
	if preview.TotalRows != 2 {
		t.Fatalf("preview total_rows = %d, want 2", preview.TotalRows)
	}
	if preview.SuggestedMap["title"] != "title" {
		t.Fatalf("expected title to auto-map to title, got %+v", preview.SuggestedMap)
	}

	importReq := multipartFileRequestWithField(t, "/api/collections/notes2/import", "file", "notes.json", exportRec.Body.Bytes(), "mapping", `{"title":"title","id":"","owner_id":"","created":"","updated":""}`)
	importReq.Header.Set("Authorization", "Bearer "+adminToken)
	importRec := httptest.NewRecorder()
	srv.Router().ServeHTTP(importRec, importReq)
	if importRec.Code != http.StatusOK {
		t.Fatalf("import: status = %d, body = %s", importRec.Code, importRec.Body.String())
	}
	var importResp struct {
		Imported int `json:"imported"`
		Failed   int `json:"failed"`
	}
	json.Unmarshal(importRec.Body.Bytes(), &importResp)
	if importResp.Imported != 2 {
		t.Fatalf("imported = %d, want 2 (failed=%d, body=%s)", importResp.Imported, importResp.Failed, importRec.Body.String())
	}
}

// TestImportSkipsDuplicatesByConflictField is the RC3 pin for import
// conflict handling: re-importing the same file with conflict_field=email
// and conflict_strategy=skip must not create duplicate records — the
// second row (matching an existing email) is skipped, not created.
func TestImportSkipsDuplicatesByConflictField(t *testing.T) {
	srv, _ := newTestServer(t)
	adminToken := bootstrapAdmin(t, srv)
	rec := doAuth(t, srv, http.MethodPost, "/api/collections", adminToken, map[string]any{
		"name":   "contacts",
		"schema": Schema{Fields: []Field{{Name: "email", Type: FieldText, Required: true}, {Name: "name", Type: FieldText}}},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create collection: status = %d, body = %s", rec.Code, rec.Body.String())
	}

	payload := []byte(`[{"email":"a@example.com","name":"Alice"},{"email":"b@example.com","name":"Bob"}]`)
	mapping := `{"email":"email","name":"name"}`

	// First import: both rows are new.
	first := multipartFileRequestWithField(t, "/api/collections/contacts/import", "file", "contacts.json", payload, "mapping", mapping)
	first.Header.Set("Authorization", "Bearer "+adminToken)
	firstRec := httptest.NewRecorder()
	srv.Router().ServeHTTP(firstRec, first)
	if firstRec.Code != http.StatusOK {
		t.Fatalf("first import: status = %d, body = %s", firstRec.Code, firstRec.Body.String())
	}
	var firstResp struct{ Imported, Skipped int }
	json.Unmarshal(firstRec.Body.Bytes(), &firstResp)
	if firstResp.Imported != 2 {
		t.Fatalf("first import: imported = %d, want 2", firstResp.Imported)
	}

	// Second import of the exact same file, with conflict handling on
	// email/skip: both rows already exist, so nothing new should be
	// created. multipartFileRequestWithField only supports one extra
	// field, not enough for mapping + conflict_field + conflict_strategy
	// together — multipartImportRequest below takes any number.
	form := multipartImportRequest(t, "/api/collections/contacts/import", payload, map[string]string{
		"mapping":           mapping,
		"conflict_field":    "email",
		"conflict_strategy": "skip",
	})
	form.Header.Set("Authorization", "Bearer "+adminToken)
	secondRec := httptest.NewRecorder()
	srv.Router().ServeHTTP(secondRec, form)
	if secondRec.Code != http.StatusOK {
		t.Fatalf("second import: status = %d, body = %s", secondRec.Code, secondRec.Body.String())
	}
	var secondResp struct{ Imported, Skipped, Updated int }
	json.Unmarshal(secondRec.Body.Bytes(), &secondResp)
	if secondResp.Skipped != 2 || secondResp.Imported != 0 {
		t.Fatalf("second import = %+v, want 0 imported, 2 skipped (both already exist)", secondResp)
	}

	listRec := doAuth(t, srv, http.MethodGet, "/api/collections/contacts/records", adminToken, nil)
	var listResp struct {
		Items []map[string]any `json:"items"`
	}
	json.Unmarshal(listRec.Body.Bytes(), &listResp)
	if len(listResp.Items) != 2 {
		t.Fatalf("expected still exactly 2 records (no duplicates created), got %d", len(listResp.Items))
	}
}

// TestImportReplaceStrategyUpdatesExistingRecord confirms
// conflict_strategy=replace updates the matched record in place instead
// of skipping or duplicating it.
func TestImportReplaceStrategyUpdatesExistingRecord(t *testing.T) {
	srv, _ := newTestServer(t)
	adminToken := bootstrapAdmin(t, srv)
	doAuth(t, srv, http.MethodPost, "/api/collections", adminToken, map[string]any{
		"name":   "contacts",
		"schema": Schema{Fields: []Field{{Name: "email", Type: FieldText, Required: true}, {Name: "name", Type: FieldText}}},
	})
	doAuth(t, srv, http.MethodPost, "/api/collections/contacts/records", adminToken, map[string]any{"email": "a@example.com", "name": "Old Name"})

	payload := []byte(`[{"email":"a@example.com","name":"New Name"}]`)
	form := multipartImportRequest(t, "/api/collections/contacts/import", payload, map[string]string{
		"mapping":           `{"email":"email","name":"name"}`,
		"conflict_field":    "email",
		"conflict_strategy": "replace",
	})
	form.Header.Set("Authorization", "Bearer "+adminToken)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, form)
	if rec.Code != http.StatusOK {
		t.Fatalf("import: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp struct{ Imported, Updated, Skipped int }
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Updated != 1 || resp.Imported != 0 {
		t.Fatalf("import = %+v, want 1 updated, 0 imported", resp)
	}

	listRec := doAuth(t, srv, http.MethodGet, "/api/collections/contacts/records", adminToken, nil)
	var listResp struct {
		Items []map[string]any `json:"items"`
	}
	json.Unmarshal(listRec.Body.Bytes(), &listResp)
	if len(listResp.Items) != 1 {
		t.Fatalf("expected exactly 1 record (updated in place, not duplicated), got %d", len(listResp.Items))
	}
	if listResp.Items[0]["name"] != "New Name" {
		t.Fatalf("record name = %v, want it replaced with New Name", listResp.Items[0]["name"])
	}
}

// TestAtomicImportRollsBackOnAnyFailure is the RC3 pin for atomic import:
// when one row in the file is invalid, atomic=true must import nothing at
// all — not the valid rows before it, matching "rollback on failure"
// rather than the default best-effort partial-success behavior.
func TestAtomicImportRollsBackOnAnyFailure(t *testing.T) {
	srv, _ := newTestServer(t)
	adminToken := bootstrapAdmin(t, srv)
	doAuth(t, srv, http.MethodPost, "/api/collections", adminToken, map[string]any{
		"name":   "contacts",
		"schema": Schema{Fields: []Field{{Name: "email", Type: FieldText, Required: true}}},
	})

	// Row 2 is missing the required "email" field entirely.
	payload := []byte(`[{"email":"a@example.com"},{"nope":"missing required field"}]`)
	form := multipartImportRequest(t, "/api/collections/contacts/import", payload, map[string]string{
		"mapping": `{"email":"email"}`,
		"atomic":  "true",
	})
	form.Header.Set("Authorization", "Bearer "+adminToken)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, form)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("atomic import with a bad row: status = %d, want 400, body = %s", rec.Code, rec.Body.String())
	}

	listRec := doAuth(t, srv, http.MethodGet, "/api/collections/contacts/records", adminToken, nil)
	var listResp struct {
		Items []map[string]any `json:"items"`
	}
	json.Unmarshal(listRec.Body.Bytes(), &listResp)
	if len(listResp.Items) != 0 {
		t.Fatalf("atomic import must create nothing when any row fails, got %d record(s)", len(listResp.Items))
	}
}

// multipartImportRequest builds a multipart request with a file field plus
// any number of extra plain form fields — multipartFileRequestWithField
// only supports one extra field, not enough for conflict_field +
// conflict_strategy + atomic together.
func multipartImportRequest(t *testing.T, path string, content []byte, fields map[string]string) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", "import.json")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("write content: %v", err)
	}
	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			t.Fatalf("write field %s: %v", k, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	return req
}

func TestFullBackupExportAndRestore(t *testing.T) {
	srv, _ := newTestServer(t)
	adminToken := bootstrapAdmin(t, srv)
	createTestCollection(t, srv, adminToken, "notes")
	doAuth(t, srv, http.MethodPost, "/api/collections/notes/records", adminToken, map[string]any{"title": "keep me"})

	exportRec := doAuth(t, srv, http.MethodGet, "/api/backups/export", adminToken, nil)
	if exportRec.Code != http.StatusOK {
		t.Fatalf("backup export: status = %d, body = %s", exportRec.Code, exportRec.Body.String())
	}
	backupZip := exportRec.Body.Bytes()
	if len(backupZip) == 0 {
		t.Fatalf("backup zip is empty")
	}

	// Mutate state after the backup, then restore and confirm it's undone.
	doAuth(t, srv, http.MethodPost, "/api/collections/notes/records", adminToken, map[string]any{"title": "should disappear after restore"})

	restoreReq := multipartUploadRequest(t, "/api/backups/import", "file", "backup.zip", backupZip)
	restoreReq.Header.Set("Authorization", "Bearer "+adminToken)
	restoreRec := httptest.NewRecorder()
	srv.Router().ServeHTTP(restoreRec, restoreReq)
	if restoreRec.Code != http.StatusOK {
		t.Fatalf("restore: status = %d, body = %s", restoreRec.Code, restoreRec.Body.String())
	}

	listRec := doAuth(t, srv, http.MethodGet, "/api/collections/notes/records", adminToken, nil)
	var listResp struct {
		Items []map[string]any `json:"items"`
	}
	json.Unmarshal(listRec.Body.Bytes(), &listResp)
	if len(listResp.Items) != 1 {
		t.Fatalf("expected 1 record after restore, got %d: %+v", len(listResp.Items), listResp.Items)
	}
}

// TestImportRejectsOversizedUpload is the request-size-cap regression test
// (security-audit Fix 6): parseImportFile used to call
// r.ParseMultipartForm directly with no http.MaxBytesReader wrapping at
// all, unlike every other upload handler in this codebase — an import
// request had no server-side size limit. It now wraps r.Body in
// http.MaxBytesReader(w, r.Body, s.cfg.MaxUploadSize) before parsing,
// mirroring file_handlers.go's handleUploadFile exactly.
func TestImportRejectsOversizedUpload(t *testing.T) {
	srv, _ := newTestServerWithConfig(t, config.Config{MaxUploadSize: 64})
	adminToken := bootstrapAdmin(t, srv)
	createTestCollection(t, srv, adminToken, "notes")

	oversized := []byte(`[{"title":"` + strings.Repeat("x", 200) + `"}]`)
	req := multipartUploadRequest(t, "/api/collections/notes/import/preview", "file", "notes.json", oversized)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
	}
	var env errorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if env.Code != "invalid_upload" {
		t.Fatalf("code = %q, want %q", env.Code, "invalid_upload")
	}
}

// TestCSVExportEscapesFormulaInjection is the CSV-formula-injection
// regression test (security-audit Fix 8): a record field value starting
// with =, +, -, or @ used to be written into the exported CSV verbatim,
// which Excel/Sheets interprets as a live formula the moment the file is
// opened. csvCell now prefixes such a value with a leading single quote —
// the standard mitigation, invisible in the rendered cell but enough to
// stop the spreadsheet application from evaluating it as a formula.
func TestCSVExportEscapesFormulaInjection(t *testing.T) {
	srv, _ := newTestServer(t)
	adminToken := bootstrapAdmin(t, srv)
	createTestCollection(t, srv, adminToken, "notes")

	doAuth(t, srv, http.MethodPost, "/api/collections/notes/records", adminToken, map[string]any{
		"title": `=HYPERLINK("http://evil.example","click me")`,
	})
	doAuth(t, srv, http.MethodPost, "/api/collections/notes/records", adminToken, map[string]any{
		"title": "an ordinary title",
	})

	rec := doAuth(t, srv, http.MethodGet, "/api/collections/notes/export?format=csv", adminToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("export: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, `,=HYPERLINK`) || strings.Contains(body, `"=HYPERLINK`) {
		t.Fatalf("exported CSV must never contain a live, unescaped formula (a cell starting with '='): %s", body)
	}
	if !strings.Contains(body, `'=HYPERLINK`) {
		t.Fatalf("expected the formula-like value to be neutralized with a leading quote, got: %s", body)
	}
	if !strings.Contains(body, "an ordinary title") {
		t.Fatalf("expected the harmless value to still be present untouched: %s", body)
	}
}

func multipartFileRequestWithField(t *testing.T, path, fieldName, filename string, content []byte, extraField, extraValue string) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile(fieldName, filename)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("write content: %v", err)
	}
	if extraField != "" {
		if err := w.WriteField(extraField, extraValue); err != nil {
			t.Fatalf("write field: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	return req
}
