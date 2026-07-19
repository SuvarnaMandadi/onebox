package server

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
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
