package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// bootstrapAdmin creates the first admin and returns its session token.
func bootstrapAdmin(t *testing.T, srv *Server) string {
	t.Helper()
	rec := doJSON(t, srv, http.MethodPost, "/api/admins/signup", authRequest{Email: "admin@example.com", Password: "hunter22222"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("bootstrap admin failed: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp adminAuthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode admin signup response: %v", err)
	}
	return resp.Token
}

// signupUser creates a _users account and returns (id, token).
func signupUser(t *testing.T, srv *Server, email string) (string, string) {
	t.Helper()
	rec := doJSON(t, srv, http.MethodPost, "/api/auth/signup", authRequest{Email: email, Password: "hunter22222"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("signup user failed: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp authResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode signup response: %v", err)
	}
	return resp.Record.ID, resp.Token
}

func doAuth(t *testing.T, srv *Server, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := jsonRequest(t, method, path, body)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	srv.Router().ServeHTTP(rec, req)
	return rec
}

func jsonRequest(t *testing.T, method, path string, body any) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestCreateCollection(t *testing.T) {
	tests := []struct {
		name       string
		useAdmin   bool
		body       createCollectionRequest
		wantStatus int
		wantCode   string
	}{
		{
			name:     "valid collection as admin",
			useAdmin: true,
			body: createCollectionRequest{
				Name:   "posts",
				Schema: Schema{Fields: []Field{{Name: "title", Type: FieldText, Required: true}}},
			},
			wantStatus: http.StatusCreated,
		},
		{
			name:     "rejects without admin auth",
			useAdmin: false,
			body: createCollectionRequest{
				Name:   "posts",
				Schema: Schema{Fields: []Field{{Name: "title", Type: FieldText, Required: true}}},
			},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:     "rejects bad field type",
			useAdmin: true,
			body: createCollectionRequest{
				Name:   "posts",
				Schema: Schema{Fields: []Field{{Name: "title", Type: "not-a-type"}}},
			},
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_collection",
		},
		{
			name:     "rejects reserved collection name",
			useAdmin: true,
			body: createCollectionRequest{
				Name:   "_users",
				Schema: Schema{Fields: []Field{{Name: "title", Type: FieldText}}},
			},
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_collection",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, _ := newTestServer(t)
			token := ""
			if tt.useAdmin {
				token = bootstrapAdmin(t, srv)
			}

			rec := doAuth(t, srv, http.MethodPost, "/api/collections", token, tt.body)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d, body = %s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if tt.wantCode != "" {
				var env errorEnvelope
				if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
					t.Fatalf("decode error: %v", err)
				}
				if env.Code != tt.wantCode {
					t.Fatalf("code = %q, want %q", env.Code, tt.wantCode)
				}
			}
		})
	}
}

func TestCreateCollectionDuplicateName(t *testing.T) {
	srv, _ := newTestServer(t)
	token := bootstrapAdmin(t, srv)
	body := createCollectionRequest{Name: "posts", Schema: Schema{Fields: []Field{{Name: "title", Type: FieldText}}}}

	first := doAuth(t, srv, http.MethodPost, "/api/collections", token, body)
	if first.Code != http.StatusCreated {
		t.Fatalf("first create failed: status = %d, body = %s", first.Code, first.Body.String())
	}

	second := doAuth(t, srv, http.MethodPost, "/api/collections", token, body)
	if second.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d, body = %s", second.Code, http.StatusConflict, second.Body.String())
	}
}

func TestGetAndDeleteCollection(t *testing.T) {
	srv, _ := newTestServer(t)
	token := bootstrapAdmin(t, srv)
	body := createCollectionRequest{Name: "posts", Schema: Schema{Fields: []Field{{Name: "title", Type: FieldText}}}}
	create := doAuth(t, srv, http.MethodPost, "/api/collections", token, body)
	if create.Code != http.StatusCreated {
		t.Fatalf("create failed: status = %d, body = %s", create.Code, create.Body.String())
	}

	get := doAuth(t, srv, http.MethodGet, "/api/collections/posts", token, nil)
	if get.Code != http.StatusOK {
		t.Fatalf("get status = %d, want 200, body = %s", get.Code, get.Body.String())
	}

	getMissing := doAuth(t, srv, http.MethodGet, "/api/collections/nope", token, nil)
	if getMissing.Code != http.StatusNotFound {
		t.Fatalf("get missing status = %d, want 404", getMissing.Code)
	}

	del := doAuth(t, srv, http.MethodDelete, "/api/collections/posts", token, nil)
	if del.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204, body = %s", del.Code, del.Body.String())
	}

	getAfterDelete := doAuth(t, srv, http.MethodGet, "/api/collections/posts", token, nil)
	if getAfterDelete.Code != http.StatusNotFound {
		t.Fatalf("get after delete status = %d, want 404", getAfterDelete.Code)
	}
}
