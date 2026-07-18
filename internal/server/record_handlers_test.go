package server

import (
	"encoding/json"
	"net/http"
	"testing"
)

// setupPostsCollection creates a "posts" collection with default rules
// (list/view/create=authenticated, update/delete=owner) and returns the
// admin token.
func setupPostsCollection(t *testing.T, srv *Server, rules Rules) {
	t.Helper()
	token := bootstrapAdmin(t, srv)
	body := createCollectionRequest{
		Name: "posts",
		Schema: Schema{Fields: []Field{
			{Name: "title", Type: FieldText, Required: true},
			{Name: "published", Type: FieldBool},
			{Name: "views", Type: FieldNumber},
			{Name: "meta", Type: FieldJSON},
		}},
		Rules: rules,
	}
	rec := doAuth(t, srv, http.MethodPost, "/api/collections", token, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create posts collection failed: status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestCreateRecord(t *testing.T) {
	tests := []struct {
		name       string
		auth       bool
		body       map[string]any
		wantStatus int
		wantCode   string
	}{
		{
			name:       "authenticated create succeeds",
			auth:       true,
			body:       map[string]any{"title": "hello world"},
			wantStatus: http.StatusCreated,
		},
		{
			name:       "unauthenticated create rejected",
			auth:       false,
			body:       map[string]any{"title": "hello world"},
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "missing required field",
			auth:       true,
			body:       map[string]any{"published": true},
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_record",
		},
		{
			name:       "wrong field type",
			auth:       true,
			body:       map[string]any{"title": "ok", "views": "not-a-number"},
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_record",
		},
		{
			name:       "unknown field rejected",
			auth:       true,
			body:       map[string]any{"title": "ok", "nope": 1},
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_record",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, _ := newTestServer(t)
			setupPostsCollection(t, srv, DefaultRules())

			token := ""
			if tt.auth {
				_, token = signupUser(t, srv, "writer@example.com")
			}

			rec := doAuth(t, srv, http.MethodPost, "/api/collections/posts/records", token, tt.body)

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

func TestRecordOwnership(t *testing.T) {
	srv, _ := newTestServer(t)
	setupPostsCollection(t, srv, DefaultRules()) // update/delete = owner

	_, ownerToken := signupUser(t, srv, "owner@example.com")
	_, otherToken := signupUser(t, srv, "other@example.com")

	createRec := doAuth(t, srv, http.MethodPost, "/api/collections/posts/records", ownerToken, map[string]any{"title": "mine"})
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create failed: status = %d, body = %s", createRec.Code, createRec.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created record: %v", err)
	}
	id := created["id"].(string)

	t.Run("owner can update", func(t *testing.T) {
		rec := doAuth(t, srv, http.MethodPatch, "/api/collections/posts/records/"+id, ownerToken, map[string]any{"title": "updated"})
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("non-owner update rejected as not found", func(t *testing.T) {
		rec := doAuth(t, srv, http.MethodPatch, "/api/collections/posts/records/"+id, otherToken, map[string]any{"title": "hijacked"})
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404, body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("non-owner delete rejected as not found", func(t *testing.T) {
		rec := doAuth(t, srv, http.MethodDelete, "/api/collections/posts/records/"+id, otherToken, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404, body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("owner can delete", func(t *testing.T) {
		rec := doAuth(t, srv, http.MethodDelete, "/api/collections/posts/records/"+id, ownerToken, nil)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204, body = %s", rec.Code, rec.Body.String())
		}
		getRec := doAuth(t, srv, http.MethodGet, "/api/collections/posts/records/"+id, ownerToken, nil)
		if getRec.Code != http.StatusNotFound {
			t.Fatalf("get after delete status = %d, want 404", getRec.Code)
		}
	})
}

func TestListRecordsRules(t *testing.T) {
	t.Run("public list rule allows unauthenticated", func(t *testing.T) {
		srv, _ := newTestServer(t)
		rules := DefaultRules()
		rules.List = RulePublic
		setupPostsCollection(t, srv, rules)

		_, token := signupUser(t, srv, "writer@example.com")
		doAuth(t, srv, http.MethodPost, "/api/collections/posts/records", token, map[string]any{"title": "public post"})

		rec := doAuth(t, srv, http.MethodGet, "/api/collections/posts/records", "", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("authenticated list rule rejects unauthenticated", func(t *testing.T) {
		srv, _ := newTestServer(t)
		setupPostsCollection(t, srv, DefaultRules())

		rec := doAuth(t, srv, http.MethodGet, "/api/collections/posts/records", "", nil)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403, body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("owner list rule scopes results to caller", func(t *testing.T) {
		srv, _ := newTestServer(t)
		rules := DefaultRules()
		rules.List = RuleOwner
		setupPostsCollection(t, srv, rules)

		_, tokenA := signupUser(t, srv, "a@example.com")
		_, tokenB := signupUser(t, srv, "b@example.com")

		doAuth(t, srv, http.MethodPost, "/api/collections/posts/records", tokenA, map[string]any{"title": "a's post"})
		doAuth(t, srv, http.MethodPost, "/api/collections/posts/records", tokenB, map[string]any{"title": "b's post"})

		rec := doAuth(t, srv, http.MethodGet, "/api/collections/posts/records", tokenA, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
		}
		var resp struct {
			Items []map[string]any `json:"items"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if len(resp.Items) != 1 {
			t.Fatalf("got %d items, want 1 (owner-scoped)", len(resp.Items))
		}
		if resp.Items[0]["title"] != "a's post" {
			t.Fatalf("got title %v, want \"a's post\"", resp.Items[0]["title"])
		}
	})
}

func TestListRecordsPaginationAndFilter(t *testing.T) {
	srv, _ := newTestServer(t)
	rules := DefaultRules()
	rules.List = RulePublic
	setupPostsCollection(t, srv, rules)

	_, token := signupUser(t, srv, "writer@example.com")
	titles := []string{"one", "two", "three"}
	for _, title := range titles {
		rec := doAuth(t, srv, http.MethodPost, "/api/collections/posts/records", token, map[string]any{"title": title, "published": title == "two"})
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %q failed: status = %d, body = %s", title, rec.Code, rec.Body.String())
		}
	}

	t.Run("paginates with limit and cursor", func(t *testing.T) {
		seen := map[string]bool{}
		cursor := ""
		for i := 0; i < 3; i++ {
			path := "/api/collections/posts/records?limit=1"
			if cursor != "" {
				path += "&cursor=" + cursor
			}
			rec := doAuth(t, srv, http.MethodGet, path, "", nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("page %d status = %d, body = %s", i, rec.Code, rec.Body.String())
			}
			var resp struct {
				Items      []map[string]any `json:"items"`
				NextCursor string           `json:"nextCursor"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if len(resp.Items) != 1 {
				t.Fatalf("page %d: got %d items, want 1", i, len(resp.Items))
			}
			id := resp.Items[0]["id"].(string)
			if seen[id] {
				t.Fatalf("page %d: saw duplicate record id %s across pages", i, id)
			}
			seen[id] = true
			cursor = resp.NextCursor
			if i < 2 && cursor == "" {
				t.Fatalf("page %d: expected nextCursor, got none", i)
			}
		}
		if len(seen) != 3 {
			t.Fatalf("saw %d unique records across pages, want 3", len(seen))
		}
	})

	t.Run("filters by field value", func(t *testing.T) {
		rec := doAuth(t, srv, http.MethodGet, "/api/collections/posts/records?filter=title=two", "", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
		}
		var resp struct {
			Items []map[string]any `json:"items"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if len(resp.Items) != 1 || resp.Items[0]["title"] != "two" {
			t.Fatalf("got items %+v, want exactly one record titled \"two\"", resp.Items)
		}
	})
}

func TestJSONFieldRoundTrip(t *testing.T) {
	srv, _ := newTestServer(t)
	rules := DefaultRules()
	rules.List = RulePublic
	setupPostsCollection(t, srv, rules)

	_, token := signupUser(t, srv, "writer@example.com")
	meta := map[string]any{"tags": []any{"go", "sqlite"}, "priority": float64(2)}

	rec := doAuth(t, srv, http.MethodPost, "/api/collections/posts/records", token, map[string]any{"title": "with meta", "meta": meta})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create failed: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created record: %v", err)
	}

	gotMeta, ok := created["meta"].(map[string]any)
	if !ok {
		t.Fatalf("meta field = %T %v, want decoded JSON object", created["meta"], created["meta"])
	}
	if gotMeta["priority"] != float64(2) {
		t.Fatalf("meta.priority = %v, want 2", gotMeta["priority"])
	}
}

// TestMixedCaseCollectionFieldsAndValues is an end-to-end regression test
// for a real user-reported bug: capital letters were rejected in
// collection names and field names (over-strict regex), even though field
// *values* were never restricted. Exercises capitals in all three: the
// collection name, a field name, and the value written to it.
func TestMixedCaseCollectionFieldsAndValues(t *testing.T) {
	srv, _ := newTestServer(t)
	adminToken := bootstrapAdmin(t, srv)

	createRec := doAuth(t, srv, http.MethodPost, "/api/collections", adminToken, createCollectionRequest{
		Name: "JobApplications",
		Schema: Schema{Fields: []Field{
			{Name: "candidateName", Type: FieldText, Required: true},
			{Name: "Status", Type: FieldText},
		}},
		Rules: DefaultRules(),
	})
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create collection failed: status = %d, body = %s", createRec.Code, createRec.Body.String())
	}

	_, userToken := signupUser(t, srv, "Recruiter@Example.com")

	createRecordRec := doAuth(t, srv, http.MethodPost, "/api/collections/JobApplications/records", userToken, map[string]any{
		"candidateName": "Suvarna Mandadi",
		"Status":        "Interviewing",
	})
	if createRecordRec.Code != http.StatusCreated {
		t.Fatalf("create record failed: status = %d, body = %s", createRecordRec.Code, createRecordRec.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(createRecordRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created record: %v", err)
	}
	if created["candidateName"] != "Suvarna Mandadi" {
		t.Fatalf("candidateName = %v, want %q", created["candidateName"], "Suvarna Mandadi")
	}
	if created["Status"] != "Interviewing" {
		t.Fatalf("Status = %v, want %q", created["Status"], "Interviewing")
	}

	listRec := doAuth(t, srv, http.MethodGet, "/api/collections/JobApplications/records", userToken, nil)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list failed: status = %d, body = %s", listRec.Code, listRec.Body.String())
	}
}
