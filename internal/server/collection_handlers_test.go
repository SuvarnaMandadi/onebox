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
		{
			// Real user-reported bug: collection/field names with capital
			// letters (e.g. "Posts", "userEmail") were rejected outright.
			name:     "allows mixed-case collection and field names",
			useAdmin: true,
			body: createCollectionRequest{
				Name:   "Posts",
				Schema: Schema{Fields: []Field{{Name: "userEmail", Type: FieldText, Required: true}}},
			},
			wantStatus: http.StatusCreated,
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

// TestSlugifyCollectionName is the pure-function pin for RC2's "allow
// spaces in collection names" fix — a human-typed name is turned into a
// legal internal identifier rather than rejected outright.
func TestSlugifyCollectionName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Customer Details", "customer_details"},
		{"  leading and trailing  ", "leading_and_trailing"},
		{"Multi   Space", "multi_space"},
		{"Weird!!Punctuation??", "weird_punctuation"},
		{"posts", "posts"},             // already legal — untouched, case preserved
		{"Posts", "Posts"},             // already legal (nameRE allows mixed case) — untouched
		{"2024 Report", "2024 Report"}, // digit-led even after slugifying — can't be fixed, left as-is
		{"_users", "_users"},           // would slugify to "users", laundering a reserved name — refused
		{"_Users!!", "_Users!!"},       // same, case/punctuation variant — still refused
	}
	for _, tc := range cases {
		if got := SlugifyCollectionName(tc.in); got != tc.want {
			t.Errorf("SlugifyCollectionName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestCreateCollectionAllowsSpacesInName is the end-to-end pin: a request
// naming a collection "Customer Details" must succeed and register a
// collection actually named "customer_details" — not reject the request,
// and not silently keep the raw, SQL-identifier-illegal name.
func TestCreateCollectionAllowsSpacesInName(t *testing.T) {
	srv, _ := newTestServer(t)
	token := bootstrapAdmin(t, srv)
	body := createCollectionRequest{Name: "Customer Details", Schema: Schema{Fields: []Field{{Name: "email", Type: FieldText}}}}

	rec := doAuth(t, srv, http.MethodPost, "/api/collections", token, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body = %s", rec.Code, rec.Body.String())
	}
	var c collection
	if err := json.Unmarshal(rec.Body.Bytes(), &c); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if c.Name != "customer_details" {
		t.Fatalf("collection Name = %q, want %q", c.Name, "customer_details")
	}

	// And it's genuinely usable under that name — not just returned once.
	get := doAuth(t, srv, http.MethodGet, "/api/collections/customer_details", token, nil)
	if get.Code != http.StatusOK {
		t.Fatalf("GET /api/collections/customer_details: status = %d, body = %s", get.Code, get.Body.String())
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

func TestUpdateCollectionSchema(t *testing.T) {
	srv, _ := newTestServer(t)
	token := bootstrapAdmin(t, srv)

	create := doAuth(t, srv, http.MethodPost, "/api/collections", token, createCollectionRequest{
		Name: "posts",
		Schema: Schema{Fields: []Field{
			{Name: "title", Type: FieldText, Required: true},
			{Name: "views", Type: FieldNumber},
		}},
	})
	if create.Code != http.StatusCreated {
		t.Fatalf("create failed: status = %d, body = %s", create.Code, create.Body.String())
	}

	rec1 := doAuth(t, srv, http.MethodPost, "/api/collections/posts/records", token, map[string]any{"title": "hello", "views": 3})
	if rec1.Code != http.StatusCreated {
		t.Fatalf("create record failed: status = %d, body = %s", rec1.Code, rec1.Body.String())
	}

	// Rename "title" -> "headline", drop "views", add "published" (bool).
	update := doAuth(t, srv, http.MethodPatch, "/api/collections/posts", token, updateCollectionSchemaRequest{
		Fields: []Field{
			{Name: "headline", Type: FieldText, Required: true, RenameFrom: "title"},
			{Name: "published", Type: FieldBool},
		},
	})
	if update.Code != http.StatusOK {
		t.Fatalf("update schema failed: status = %d, body = %s", update.Code, update.Body.String())
	}
	var updated collection
	if err := json.Unmarshal(update.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode updated collection: %v", err)
	}
	if len(updated.Schema.Fields) != 2 || updated.Schema.Fields[0].Name != "headline" || updated.Schema.Fields[0].RenameFrom != "" {
		t.Fatalf("unexpected schema after update: %+v", updated.Schema.Fields)
	}

	// Existing data should have survived the rename under the new field name.
	list := doAuth(t, srv, http.MethodGet, "/api/collections/posts/records", token, nil)
	if list.Code != http.StatusOK {
		t.Fatalf("list records failed: status = %d, body = %s", list.Code, list.Body.String())
	}
	var listResp struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode records: %v", err)
	}
	if len(listResp.Items) != 1 || listResp.Items[0]["headline"] != "hello" {
		t.Fatalf("expected renamed field to carry data over, got %+v", listResp.Items)
	}
	if _, stillHasViews := listResp.Items[0]["views"]; stillHasViews {
		t.Fatalf("dropped field %q should not appear in record output: %+v", "views", listResp.Items[0])
	}

	notFound := doAuth(t, srv, http.MethodPatch, "/api/collections/nope", token, updateCollectionSchemaRequest{
		Fields: []Field{{Name: "x", Type: FieldText}},
	})
	if notFound.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body = %s", notFound.Code, notFound.Body.String())
	}

	rejectsNoAuth := doAuth(t, srv, http.MethodPatch, "/api/collections/posts", "", updateCollectionSchemaRequest{
		Fields: []Field{{Name: "x", Type: FieldText}},
	})
	if rejectsNoAuth.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401, body = %s", rejectsNoAuth.Code, rejectsNoAuth.Body.String())
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

// TestCreateCollectionWithRelationField is the Milestone 6 happy-path pin:
// a relation field whose target collection already exists is accepted, and
// the persisted schema carries relation_collection back out.
func TestCreateCollectionWithRelationField(t *testing.T) {
	srv, _ := newTestServer(t)
	token := bootstrapAdmin(t, srv)

	create := doAuth(t, srv, http.MethodPost, "/api/collections", token, createCollectionRequest{
		Name:   "customers",
		Schema: Schema{Fields: []Field{{Name: "name", Type: FieldText, Required: true}}},
	})
	if create.Code != http.StatusCreated {
		t.Fatalf("create customers failed: status = %d, body = %s", create.Code, create.Body.String())
	}

	ordersReq := createCollectionRequest{
		Name: "orders",
		Schema: Schema{Fields: []Field{
			{Name: "total", Type: FieldNumber, Required: true},
			{Name: "customer_id", Type: FieldRelation, RelationCollection: "customers"},
		}},
	}
	orders := doAuth(t, srv, http.MethodPost, "/api/collections", token, ordersReq)
	if orders.Code != http.StatusCreated {
		t.Fatalf("create orders failed: status = %d, body = %s", orders.Code, orders.Body.String())
	}
	var c collection
	if err := json.Unmarshal(orders.Body.Bytes(), &c); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var relField *Field
	for i, f := range c.Schema.Fields {
		if f.Name == "customer_id" {
			relField = &c.Schema.Fields[i]
		}
	}
	if relField == nil || relField.Type != FieldRelation || relField.RelationCollection != "customers" {
		t.Fatalf("expected a persisted relation field pointing at customers, got %+v", c.Schema.Fields)
	}
}

// TestCreateCollectionRelationTargetMustExist confirms a relation field
// naming a nonexistent collection is rejected at creation time (schema-
// level validation, validateRelationTargets in collections.go) rather than
// silently accepted and only failing later at record-write time.
func TestCreateCollectionRelationTargetMustExist(t *testing.T) {
	srv, _ := newTestServer(t)
	token := bootstrapAdmin(t, srv)

	rec := doAuth(t, srv, http.MethodPost, "/api/collections", token, createCollectionRequest{
		Name: "orders",
		Schema: Schema{Fields: []Field{
			{Name: "customer_id", Type: FieldRelation, RelationCollection: "ghost"},
		}},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
	}
}

// TestUpdateCollectionSchemaAddsRelationField confirms a relation field can
// be added to an existing collection via update_schema (PATCH), and that a
// self-relation (target = the collection being updated) is legal — see
// validateRelationTargets' doc comment for why self-relations only work at
// update time, never at initial creation.
func TestUpdateCollectionSchemaAddsRelationField(t *testing.T) {
	srv, _ := newTestServer(t)
	token := bootstrapAdmin(t, srv)

	create := doAuth(t, srv, http.MethodPost, "/api/collections", token, createCollectionRequest{
		Name:   "employees",
		Schema: Schema{Fields: []Field{{Name: "name", Type: FieldText, Required: true}}},
	})
	if create.Code != http.StatusCreated {
		t.Fatalf("create failed: status = %d, body = %s", create.Code, create.Body.String())
	}

	update := doAuth(t, srv, http.MethodPatch, "/api/collections/employees", token, updateCollectionSchemaRequest{
		Fields: []Field{
			{Name: "name", Type: FieldText, Required: true},
			{Name: "manager_id", Type: FieldRelation, RelationCollection: "employees"},
		},
	})
	if update.Code != http.StatusOK {
		t.Fatalf("update failed: status = %d, body = %s", update.Code, update.Body.String())
	}

	// RC4 regression: updateCollectionSchema used to persist only
	// Name/Type/Required, silently dropping RelationCollection (and
	// Validation, see TestUpdateCollectionSchemaPreservesValidation below)
	// from the schema actually written to _collections — the request above
	// would return 200 even while corrupting relation_id into a dangling,
	// unlinked "relation" field. Re-fetching (rather than trusting the PATCH
	// response body) proves it round-trips through a real read, not just
	// whatever the handler happened to echo back.
	var updated collection
	if err := json.Unmarshal(update.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode updated collection: %v", err)
	}
	if len(updated.Schema.Fields) != 2 || updated.Schema.Fields[1].RelationCollection != "employees" {
		t.Fatalf("expected manager_id.relation_collection = %q to survive the update, got %+v", "employees", updated.Schema.Fields)
	}

	refetch := doAuth(t, srv, http.MethodGet, "/api/collections/employees", token, nil)
	if refetch.Code != http.StatusOK {
		t.Fatalf("refetch failed: status = %d, body = %s", refetch.Code, refetch.Body.String())
	}
	var reread collection
	if err := json.Unmarshal(refetch.Body.Bytes(), &reread); err != nil {
		t.Fatalf("decode refetched collection: %v", err)
	}
	if len(reread.Schema.Fields) != 2 || reread.Schema.Fields[1].RelationCollection != "employees" {
		t.Fatalf("expected manager_id.relation_collection = %q to still be persisted on refetch, got %+v", "employees", reread.Schema.Fields)
	}
}

// TestUpdateCollectionSchemaPreservesValidation is the Validation half of
// the same RC4 regression as TestUpdateCollectionSchemaAddsRelationField
// above: a field's validation rules (unique/format/length/pattern/range)
// must survive a schema update untouched, including for a field that
// already existed before the update and isn't even the one being changed.
func TestUpdateCollectionSchemaPreservesValidation(t *testing.T) {
	srv, _ := newTestServer(t)
	token := bootstrapAdmin(t, srv)

	create := doAuth(t, srv, http.MethodPost, "/api/collections", token, createCollectionRequest{
		Name: "customers",
		Schema: Schema{Fields: []Field{
			{Name: "email", Type: FieldText, Validation: &FieldValidation{Format: "email", Unique: true}},
		}},
	})
	if create.Code != http.StatusCreated {
		t.Fatalf("create failed: status = %d, body = %s", create.Code, create.Body.String())
	}

	// Add an unrelated field — email's own validation wasn't part of this
	// request at all, so it must still survive the rebuild untouched.
	update := doAuth(t, srv, http.MethodPatch, "/api/collections/customers", token, updateCollectionSchemaRequest{
		Fields: []Field{
			{Name: "email", Type: FieldText, Validation: &FieldValidation{Format: "email", Unique: true}},
			{Name: "phone", Type: FieldText, Validation: &FieldValidation{MinLength: intPtr(7)}},
		},
	})
	if update.Code != http.StatusOK {
		t.Fatalf("update failed: status = %d, body = %s", update.Code, update.Body.String())
	}

	refetch := doAuth(t, srv, http.MethodGet, "/api/collections/customers", token, nil)
	var reread collection
	if err := json.Unmarshal(refetch.Body.Bytes(), &reread); err != nil {
		t.Fatalf("decode refetched collection: %v", err)
	}
	if len(reread.Schema.Fields) != 2 {
		t.Fatalf("expected 2 fields, got %+v", reread.Schema.Fields)
	}
	email, phone := reread.Schema.Fields[0], reread.Schema.Fields[1]
	if email.Validation == nil || email.Validation.Format != "email" || !email.Validation.Unique {
		t.Fatalf("expected email's validation to survive the update, got %+v", email.Validation)
	}
	if phone.Validation == nil || phone.Validation.MinLength == nil || *phone.Validation.MinLength != 7 {
		t.Fatalf("expected phone's validation to persist, got %+v", phone.Validation)
	}
}
