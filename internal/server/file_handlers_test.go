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
	"onebox/internal/db"
)

// newTestServerWithFiles is like newTestServer but with a real (temp)
// FilesDir, since file storage needs to hit disk.
func newTestServerWithFiles(t *testing.T) *Server {
	t.Helper()
	sqlDB, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.Migrate(sqlDB); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })

	cfg := config.Config{
		JWTSecret:     "test-secret",
		FilesDir:      t.TempDir(),
		MaxUploadSize: 1024,
	}
	return New(cfg, sqlDB)
}

func multipartUploadRequest(t *testing.T, path, fieldName, filename string, content []byte) *http.Request {
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
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, path, &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	return req
}

func TestUploadFile(t *testing.T) {
	tests := []struct {
		name       string
		auth       bool
		content    []byte
		wantStatus int
	}{
		{name: "authenticated upload succeeds", auth: true, content: []byte("hello world"), wantStatus: http.StatusCreated},
		{name: "unauthenticated upload rejected", auth: false, content: []byte("hello world"), wantStatus: http.StatusUnauthorized},
		{name: "oversized upload rejected", auth: true, content: bytes.Repeat([]byte("x"), 2048), wantStatus: http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newTestServerWithFiles(t)
			token := ""
			if tt.auth {
				_, token = signupUser(t, srv, "uploader@example.com")
			}

			req := multipartUploadRequest(t, "/api/files", "file", "hello.txt", tt.content)
			if token != "" {
				req.Header.Set("Authorization", "Bearer "+token)
			}
			rec := httptest.NewRecorder()
			srv.Router().ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d, body = %s", rec.Code, tt.wantStatus, rec.Body.String())
			}
		})
	}
}

func TestListFiles(t *testing.T) {
	srv := newTestServerWithFiles(t)
	_, userToken := signupUser(t, srv, "uploader@example.com")
	adminToken := bootstrapAdmin(t, srv)

	for _, name := range []string{"a.txt", "b.txt"} {
		req := multipartUploadRequest(t, "/api/files", "file", name, []byte("content of "+name))
		req.Header.Set("Authorization", "Bearer "+userToken)
		rec := httptest.NewRecorder()
		srv.Router().ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("upload %s failed: status = %d, body = %s", name, rec.Code, rec.Body.String())
		}
	}

	t.Run("admin can list", func(t *testing.T) {
		rec := doAuth(t, srv, http.MethodGet, "/api/files", adminToken, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
		}
		var resp struct {
			Items []fileRecord `json:"items"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if len(resp.Items) != 2 {
			t.Fatalf("got %d items, want 2", len(resp.Items))
		}
	})

	t.Run("non-admin sees only own files", func(t *testing.T) {
		rec := doAuth(t, srv, http.MethodGet, "/api/files", userToken, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
		}
		var resp struct {
			Items []fileRecord `json:"items"`
			Total int          `json:"total"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if len(resp.Items) != 2 || resp.Total != 2 {
			t.Fatalf("got %d items (total=%d), want 2 (both owned by this user)", len(resp.Items), resp.Total)
		}
	})

	t.Run("unauthenticated rejected", func(t *testing.T) {
		rec := doAuth(t, srv, http.MethodGet, "/api/files", "", nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401, body = %s", rec.Code, rec.Body.String())
		}
	})
}

func TestServeAndDeleteFile(t *testing.T) {
	srv := newTestServerWithFiles(t)
	_, ownerToken := signupUser(t, srv, "owner@example.com")
	_, otherToken := signupUser(t, srv, "other@example.com")

	content := []byte("private contents")
	uploadReq := multipartUploadRequest(t, "/api/files", "file", "secret.txt", content)
	uploadReq.Header.Set("Authorization", "Bearer "+ownerToken)
	uploadRec := httptest.NewRecorder()
	srv.Router().ServeHTTP(uploadRec, uploadReq)
	if uploadRec.Code != http.StatusCreated {
		t.Fatalf("upload failed: status = %d, body = %s", uploadRec.Code, uploadRec.Body.String())
	}
	var uploaded fileRecord
	if err := json.Unmarshal(uploadRec.Body.Bytes(), &uploaded); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}

	t.Run("owner can fetch", func(t *testing.T) {
		rec := doAuth(t, srv, http.MethodGet, "/api/files/"+uploaded.ID, ownerToken, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
		}
		if rec.Body.String() != string(content) {
			t.Fatalf("body = %q, want %q", rec.Body.String(), string(content))
		}
	})

	t.Run("non-owner fetch rejected as not found", func(t *testing.T) {
		rec := doAuth(t, srv, http.MethodGet, "/api/files/"+uploaded.ID, otherToken, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404, body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("non-owner delete rejected as not found", func(t *testing.T) {
		rec := doAuth(t, srv, http.MethodDelete, "/api/files/"+uploaded.ID, otherToken, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404, body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("owner can delete, then file is gone", func(t *testing.T) {
		rec := doAuth(t, srv, http.MethodDelete, "/api/files/"+uploaded.ID, ownerToken, nil)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204, body = %s", rec.Code, rec.Body.String())
		}
		getRec := doAuth(t, srv, http.MethodGet, "/api/files/"+uploaded.ID, ownerToken, nil)
		if getRec.Code != http.StatusNotFound {
			t.Fatalf("get after delete status = %d, want 404", getRec.Code)
		}
	})
}

// TestServeFileAlwaysSetsNoSniff pins the blanket half of Fix 5 (security
// audit): every served file, regardless of MIME type, must carry
// X-Content-Type-Options: nosniff so a browser trusts the declared
// Content-Type instead of re-sniffing the body itself.
func TestServeFileAlwaysSetsNoSniff(t *testing.T) {
	srv := newTestServerWithFiles(t)
	_, token := signupUser(t, srv, "uploader@example.com")

	uploadReq := multipartUploadRequest(t, "/api/files", "file", "plain.txt", []byte("just plain text"))
	uploadReq.Header.Set("Authorization", "Bearer "+token)
	uploadRec := httptest.NewRecorder()
	srv.Router().ServeHTTP(uploadRec, uploadReq)
	if uploadRec.Code != http.StatusCreated {
		t.Fatalf("upload failed: status = %d, body = %s", uploadRec.Code, uploadRec.Body.String())
	}
	var uploaded fileRecord
	json.Unmarshal(uploadRec.Body.Bytes(), &uploaded)

	rec := doAuth(t, srv, http.MethodGet, "/api/files/"+uploaded.ID, token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want %q", got, "nosniff")
	}
}

// TestServeFileForcesDownloadForBrowserExecutableTypes is the stored-XSS
// regression test (security-audit Fix 5): a malicious .html upload, opened
// later via its /api/files/{id} URL, used to execute in this API's own
// origin — Content-Type was set straight from the upload-time
// http.DetectContentType classification with no protection at all. It must
// now be forced to application/octet-stream with a Content-Disposition:
// attachment header so a browser downloads rather than renders it. A
// harmless plain-text upload is the control case, proving normal files are
// unaffected — still served inline as their real type.
func TestServeFileForcesDownloadForBrowserExecutableTypes(t *testing.T) {
	srv := newTestServerWithFiles(t)
	_, token := signupUser(t, srv, "uploader@example.com")

	upload := func(filename string, content []byte) fileRecord {
		t.Helper()
		req := multipartUploadRequest(t, "/api/files", "file", filename, content)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		srv.Router().ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("upload %s failed: status = %d, body = %s", filename, rec.Code, rec.Body.String())
		}
		var fr fileRecord
		json.Unmarshal(rec.Body.Bytes(), &fr)
		return fr
	}

	t.Run("html upload is forced to download", func(t *testing.T) {
		malicious := upload("evil.html", []byte("<!DOCTYPE html><html><body><script>alert(document.cookie)</script></body></html>"))
		if malicious.Mime != "text/html; charset=utf-8" {
			t.Fatalf("precondition: uploaded Mime = %q, want a text/html classification (test assumes http.DetectContentType's exact output)", malicious.Mime)
		}

		rec := doAuth(t, srv, http.MethodGet, "/api/files/"+malicious.ID, token, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Content-Type"); got != "application/octet-stream" {
			t.Fatalf("Content-Type = %q, want %q (must never be served as renderable HTML)", got, "application/octet-stream")
		}
		if disp := rec.Header().Get("Content-Disposition"); disp == "" || !strings.Contains(disp, "attachment") {
			t.Fatalf("Content-Disposition = %q, want an attachment disposition forcing download", disp)
		}
	})

	t.Run("plain text upload is unaffected, still served inline as its real type", func(t *testing.T) {
		plain := upload("notes.txt", []byte("just some notes, nothing executable"))

		rec := doAuth(t, srv, http.MethodGet, "/api/files/"+plain.ID, token, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Content-Type"); got == "application/octet-stream" {
			t.Fatalf("Content-Type = %q — a harmless text file must not be downgraded to octet-stream", got)
		}
		if disp := rec.Header().Get("Content-Disposition"); disp != "" {
			t.Fatalf("Content-Disposition = %q, want none for a normal inline-served file", disp)
		}
	})
}
