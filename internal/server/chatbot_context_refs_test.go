package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"onebox/internal/llm"
)

func TestDescribeContextRefsCollection(t *testing.T) {
	_, db := newTestServer(t)
	srv := &Server{db: db}
	seedCollection(t, db, "orders", Field{Name: "total", Type: FieldNumber, Required: true})

	got := srv.describeContextRefs(context.Background(), []contextRefInput{{Type: "collection", Collection: "orders"}})
	if !strings.Contains(got, `"orders"`) {
		t.Fatalf("missing collection name: %q", got)
	}
	if !strings.Contains(got, "total: number (required)") {
		t.Fatalf("missing field description: %q", got)
	}
}

func TestDescribeContextRefsRecord(t *testing.T) {
	srv, db := newTestServer(t)
	adminToken := bootstrapAdmin(t, srv)
	seedCollection(t, db, "orders", Field{Name: "total", Type: FieldNumber, Required: true})

	rec := doAuth(t, srv, http.MethodPost, "/api/collections/orders/records", adminToken, map[string]any{"total": 42})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create record: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var created map[string]any
	mustUnmarshal(t, rec.Body.Bytes(), &created)
	recordID, _ := created["id"].(string)
	if recordID == "" {
		t.Fatal("expected a record id in the create response")
	}

	got := srv.describeContextRefs(context.Background(), []contextRefInput{{Type: "record", Collection: "orders", RecordID: recordID}})
	if !strings.Contains(got, `"orders"`) {
		t.Fatalf("missing collection name: %q", got)
	}
	if !strings.Contains(got, recordID) {
		t.Fatalf("missing record id: %q", got)
	}
	if !strings.Contains(got, "42") {
		t.Fatalf("missing record field value: %q", got)
	}
}

func TestDescribeContextRefsGracefulOnMissingData(t *testing.T) {
	_, db := newTestServer(t)
	srv := &Server{db: db}
	seedCollection(t, db, "orders", Field{Name: "total", Type: FieldNumber})

	cases := []struct {
		name string
		ref  contextRefInput
	}{
		{"missing collection", contextRefInput{Type: "collection", Collection: "ghost"}},
		{"missing collection for record", contextRefInput{Type: "record", Collection: "ghost", RecordID: "x"}},
		{"missing record", contextRefInput{Type: "record", Collection: "orders", RecordID: "nonexistent"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := srv.describeContextRefs(context.Background(), []contextRefInput{tc.ref})
			if got == "" {
				t.Fatal("expected a note about the missing data, got empty string")
			}
			if !strings.Contains(got, "couldn't be loaded") {
				t.Fatalf("expected a graceful not-found note, got: %q", got)
			}
		})
	}
}

func TestDescribeContextRefsUnknownTypeIgnored(t *testing.T) {
	_, db := newTestServer(t)
	srv := &Server{db: db}
	got := srv.describeContextRefs(context.Background(), []contextRefInput{{Type: "spreadsheet", Collection: "x"}})
	if strings.Contains(got, "spreadsheet") {
		t.Fatalf("unknown ref type should be silently dropped, not echoed: %q", got)
	}
}

func TestDescribeContextRefsCapsAtMax(t *testing.T) {
	_, db := newTestServer(t)
	srv := &Server{db: db}
	var refs []contextRefInput
	for i := 0; i < maxContextRefs+5; i++ {
		name := "c" + string(rune('a'+i))
		seedCollection(t, db, name, Field{Name: "x", Type: FieldText})
		refs = append(refs, contextRefInput{Type: "collection", Collection: name})
	}
	got := srv.describeContextRefs(context.Background(), refs)
	count := strings.Count(got, "Fields:")
	if count != maxContextRefs {
		t.Fatalf("described %d collections, want capped at %d", count, maxContextRefs)
	}
}

func TestDescribeConversationExcerpts(t *testing.T) {
	got := describeConversationExcerpts(nil)
	if got != "" {
		t.Fatalf("describeConversationExcerpts(nil) = %q, want empty", got)
	}

	got = describeConversationExcerpts([]conversationExcerptInput{{Title: "Yesterday's chat", Text: "we discussed the pricing page"}})
	if !strings.Contains(got, "Yesterday's chat") || !strings.Contains(got, "pricing page") {
		t.Fatalf("missing title or text: %q", got)
	}
}

func TestDescribeConversationExcerptsTruncates(t *testing.T) {
	longText := strings.Repeat("x", maxConversationExcerptChars+500)
	got := describeConversationExcerpts([]conversationExcerptInput{{Title: "long one", Text: longText}})
	if strings.Contains(got, strings.Repeat("x", maxConversationExcerptChars+1)) {
		t.Fatal("excerpt text was not truncated")
	}
	if !strings.Contains(got, "[...truncated...]") {
		t.Fatalf("expected a truncation marker: %q", got[:200])
	}
}

// TestResolveAttachmentsFallsBackForFileWithoutChatAttachmentRow is the
// regression pin for M2's "reference an existing file" feature: a file
// uploaded through the ordinary Files browser (POST /api/files), never
// through POST /api/chat-attachments, has no _chat_attachments row —
// resolveAttachments must still classify and extract it on the fly
// instead of silently skipping it.
func TestResolveAttachmentsFallsBackForFileWithoutChatAttachmentRow(t *testing.T) {
	srv := newTestServerWithFiles(t)
	adminToken := bootstrapAdmin(t, srv)

	uploadReq := multipartUploadRequest(t, "/api/files", "file", "referenced.txt", []byte("Content from a plain Files-browser upload."))
	uploadReq.Header.Set("Authorization", "Bearer "+adminToken)
	uploadRec := httptest.NewRecorder()
	srv.Router().ServeHTTP(uploadRec, uploadReq)
	if uploadRec.Code != http.StatusCreated {
		t.Fatalf("upload via /api/files: status = %d, body = %s", uploadRec.Code, uploadRec.Body.String())
	}
	var fileRec fileRecord
	mustUnmarshal(t, uploadRec.Body.Bytes(), &fileRec)

	if _, err := getChatAttachment(t.Context(), srv.db, fileRec.ID); err == nil {
		t.Fatal("test setup problem: this file must NOT have a _chat_attachments row yet")
	}

	resolved := srv.resolveAttachments(t.Context(), []string{fileRec.ID}, false)
	if len(resolved.Refs) != 1 || resolved.Refs[0].Kind != "document" {
		t.Fatalf("expected 1 document ref, got %+v", resolved.Refs)
	}
	if !strings.Contains(resolved.DocsText, "Content from a plain Files-browser upload.") {
		t.Fatalf("extracted text missing from DocsText: %q", resolved.DocsText)
	}
	if !strings.Contains(resolved.DocsText, "referenced.txt") {
		t.Fatalf("filename label missing from DocsText: %q", resolved.DocsText)
	}
}

// TestChatbotContextRefsReachTheModel is the end-to-end pin: a
// context_refs entry in the request body must produce text describing
// that collection in the actual outgoing message content.
func TestChatbotContextRefsReachTheModel(t *testing.T) {
	srv, db := newTestServer(t)
	adminToken := bootstrapAdmin(t, srv)
	seedCollection(t, db, "orders", Field{Name: "total", Type: FieldNumber, Required: true})

	fake := &fakeLLMClient{}
	bundle := *srv.providers.Load()
	bundle.llm = &llm.Router{Anthropic: fake}
	bundle.chat = chatSelection{Provider: "anthropic", Model: "claude-sonnet-5"}
	srv.providers.Store(&bundle)

	rec := doAuth(t, srv, http.MethodPost, "/api/chat", adminToken, chatbotRequest{
		Message:     "summarize this collection",
		ContextRefs: []contextRefInput{{Type: "collection", Collection: "orders"}},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	last := fake.lastMessages[len(fake.lastMessages)-1]
	if !strings.Contains(last.Content, `"orders"`) || !strings.Contains(last.Content, "total: number") {
		t.Fatalf("context ref never reached the model: %q", last.Content)
	}
}

// TestChatbotConversationExcerptsReachTheModel is the end-to-end
// counterpart for #mentioned conversations.
func TestChatbotConversationExcerptsReachTheModel(t *testing.T) {
	srv, adminToken := newChatAttachmentTestServer(t)

	fake := &fakeLLMClient{}
	bundle := *srv.providers.Load()
	bundle.llm = &llm.Router{Anthropic: fake}
	bundle.chat = chatSelection{Provider: "anthropic", Model: "claude-sonnet-5"}
	srv.providers.Store(&bundle)

	rec := doAuth(t, srv, http.MethodPost, "/api/chat", adminToken, chatbotRequest{
		Message:              "does this relate to what we discussed before?",
		ConversationExcerpts: []conversationExcerptInput{{Title: "Pricing chat", Text: "we agreed on a $9/mo tier"}},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	last := fake.lastMessages[len(fake.lastMessages)-1]
	if !strings.Contains(last.Content, "Pricing chat") || !strings.Contains(last.Content, "$9/mo tier") {
		t.Fatalf("conversation excerpt never reached the model: %q", last.Content)
	}
}

// TestChatbotContextRefsAndAttachmentsAndProposalsCoexist is the M1/M2
// regression guard: context refs, a real attachment, and a proposal-
// producing tool call must all work together in one turn without
// interfering — this milestone must not regress the previous ones.
func TestChatbotContextRefsAndAttachmentsAndProposalsCoexist(t *testing.T) {
	srv, adminToken := newChatAttachmentTestServer(t)
	seedCollection(t, srv.db, "orders", Field{Name: "total", Type: FieldNumber})
	img := uploadChatAttachment(t, srv, adminToken, "mockup.png", []byte("fake-png-bytes"))

	fake := &fakeLLMClient{
		reply:     "Here's what I found.",
		toolCalls: []llm.ToolCall{toolCall(actionCreateCollection, map[string]any{"name": "notes", "fields": []map[string]any{{"name": "body", "type": "text"}}})},
	}
	bundle := *srv.providers.Load()
	bundle.llm = &llm.Router{Anthropic: fake}
	bundle.chat = chatSelection{Provider: "anthropic", Model: "claude-sonnet-5"}
	srv.providers.Store(&bundle)

	rec := doAuth(t, srv, http.MethodPost, "/api/chat", adminToken, chatbotRequest{
		Message:              "look at all of this and propose a collection",
		AttachmentIDs:        []string{img.ID},
		ContextRefs:          []contextRefInput{{Type: "collection", Collection: "orders"}},
		ConversationExcerpts: []conversationExcerptInput{{Title: "earlier", Text: "some prior context"}},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}

	last := fake.lastMessages[len(fake.lastMessages)-1]
	if len(last.Images) != 1 {
		t.Fatalf("attachment image missing: %d images", len(last.Images))
	}
	if !strings.Contains(last.Content, `"orders"`) {
		t.Fatalf("context ref missing from content: %q", last.Content)
	}
	if !strings.Contains(last.Content, "earlier") {
		t.Fatalf("conversation excerpt missing from content: %q", last.Content)
	}

	var resp chatbotResponse
	mustUnmarshal(t, rec.Body.Bytes(), &resp)
	if len(resp.Actions) != 1 {
		t.Fatalf("expected 1 proposal alongside attachments+context, got %d: %+v", len(resp.Actions), resp.Actions)
	}
}

func mustUnmarshal(t *testing.T, data []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("unmarshal: %v (body: %s)", err, data)
	}
}
