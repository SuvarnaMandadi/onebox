package server

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
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
