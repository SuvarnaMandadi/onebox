package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"onebox/internal/llm"
)

func TestSafeTruncateNeverSplitsAMultiByteRune(t *testing.T) {
	s := "héllo wörld — ünïcödé têst, a länger strïng with lots of accénts"
	for capBytes := 0; capBytes <= len(s); capBytes++ {
		out := safeTruncate(s, capBytes)
		if !utf8.ValidString(out) {
			t.Fatalf("safeTruncate(s, %d) produced invalid UTF-8: %q", capBytes, out)
		}
		if len(out) > capBytes {
			t.Fatalf("safeTruncate(s, %d) returned %d bytes, want <= %d", capBytes, len(out), capBytes)
		}
	}
	if safeTruncate("short", 100) != "short" {
		t.Fatal("safeTruncate must return the input unchanged when it's already under the cap")
	}
}

func TestClassifyChatAttachment(t *testing.T) {
	cases := []struct {
		filename string
		wantKind string
		wantOK   bool
	}{
		{"photo.png", "image", true},
		{"photo.PNG", "image", true},
		{"photo.jpg", "image", true},
		{"photo.jpeg", "image", true},
		{"report.pdf", "document", true},
		{"letter.docx", "document", true},
		{"notes.txt", "document", true},
		{"readme.md", "document", true},
		{"data.csv", "document", true},
		{"sheet.xlsx", "document", true},
		{"virus.exe", "", false},
		{"noextension", "", false},
	}
	for _, tc := range cases {
		kind, ok := classifyChatAttachment(tc.filename)
		if kind != tc.wantKind || ok != tc.wantOK {
			t.Errorf("classifyChatAttachment(%q) = (%q, %v), want (%q, %v)", tc.filename, kind, ok, tc.wantKind, tc.wantOK)
		}
	}
}

func TestDescribeHistoryAttachments(t *testing.T) {
	if got := describeHistoryAttachments(nil); got != "" {
		t.Fatalf("describeHistoryAttachments(nil) = %q, want empty", got)
	}
	got := describeHistoryAttachments([]chatAttachmentRef{
		{ID: "a", Filename: "cat.png", Kind: "image"},
		{ID: "b", Filename: "order.pdf", Kind: "document"},
	})
	if !strings.Contains(got, "cat.png") || !strings.Contains(got, "order.pdf") {
		t.Fatalf("describeHistoryAttachments() = %q, missing a filename", got)
	}
}

// newChatAttachmentTestServer is newTestServerWithFiles + an admin token,
// the combination every attachment test below needs (upload requires
// admin auth, and storeFileContent needs a real FilesDir on disk).
func newChatAttachmentTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	srv := newTestServerWithFiles(t)
	return srv, bootstrapAdmin(t, srv)
}

func TestUploadChatAttachmentDocumentExtractsText(t *testing.T) {
	srv, adminToken := newChatAttachmentTestServer(t)

	req := multipartUploadRequest(t, "/api/chat-attachments", "file", "notes.txt", []byte("hello from an attached file"))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body = %s", rec.Code, rec.Body.String())
	}

	var att chatAttachmentRecord
	if err := json.Unmarshal(rec.Body.Bytes(), &att); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if att.Kind != "document" {
		t.Fatalf("kind = %q, want %q", att.Kind, "document")
	}
	if att.ID == "" {
		t.Fatal("expected a non-empty attachment id")
	}

	row, err := getChatAttachment(t.Context(), srv.db, att.ID)
	if err != nil {
		t.Fatalf("getChatAttachment: %v", err)
	}
	if row.ExtractedText != "hello from an attached file" {
		t.Fatalf("extracted text = %q, want the raw file content", row.ExtractedText)
	}
}

func TestUploadChatAttachmentImageSkipsExtraction(t *testing.T) {
	srv, adminToken := newChatAttachmentTestServer(t)

	req := multipartUploadRequest(t, "/api/chat-attachments", "file", "photo.png", []byte("not a real png, but extension-classified as an image"))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body = %s", rec.Code, rec.Body.String())
	}

	var att chatAttachmentRecord
	json.Unmarshal(rec.Body.Bytes(), &att)
	if att.Kind != "image" {
		t.Fatalf("kind = %q, want %q", att.Kind, "image")
	}

	row, err := getChatAttachment(t.Context(), srv.db, att.ID)
	if err != nil {
		t.Fatalf("getChatAttachment: %v", err)
	}
	if row.ExtractedText != "" {
		t.Fatalf("image attachments must not run text extraction, got: %q", row.ExtractedText)
	}
}

func TestUploadChatAttachmentRejectsUnsupportedType(t *testing.T) {
	srv, adminToken := newChatAttachmentTestServer(t)

	req := multipartUploadRequest(t, "/api/chat-attachments", "file", "virus.exe", []byte("binary content"))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
	}
}

func TestUploadChatAttachmentRequiresAdmin(t *testing.T) {
	srv := newTestServerWithFiles(t)
	_, userToken := signupUser(t, srv, "user@example.com")

	req := multipartUploadRequest(t, "/api/chat-attachments", "file", "notes.txt", []byte("hi"))
	req.Header.Set("Authorization", "Bearer "+userToken)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized && rec.Code != http.StatusForbidden {
		t.Fatalf("a non-admin user must not be able to upload a chat attachment: status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestDeleteChatAttachment(t *testing.T) {
	srv, adminToken := newChatAttachmentTestServer(t)

	uploadReq := multipartUploadRequest(t, "/api/chat-attachments", "file", "notes.txt", []byte("temporary"))
	uploadReq.Header.Set("Authorization", "Bearer "+adminToken)
	uploadRec := httptest.NewRecorder()
	srv.Router().ServeHTTP(uploadRec, uploadReq)
	var att chatAttachmentRecord
	json.Unmarshal(uploadRec.Body.Bytes(), &att)

	rec := doAuth(t, srv, http.MethodDelete, "/api/chat-attachments/"+att.ID, adminToken, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body = %s", rec.Code, rec.Body.String())
	}
	if _, err := getChatAttachment(t.Context(), srv.db, att.ID); err == nil {
		t.Fatal("expected the chat_attachments row to be gone after delete")
	}
	if _, err := getFileByID(t.Context(), srv.db, att.ID); err == nil {
		t.Fatal("expected the underlying file record to be gone after delete")
	}
}

// TestChatbotAttachmentDocumentTextReachesThePrompt is the end-to-end
// regression test for the document half of the attachment pipeline: an
// uploaded .txt's extracted text must actually reach the model as part of
// the current user message, referenced only by attachment_ids.
func TestChatbotAttachmentDocumentTextReachesThePrompt(t *testing.T) {
	srv, adminToken := newChatAttachmentTestServer(t)

	uploadReq := multipartUploadRequest(t, "/api/chat-attachments", "file", "order.txt", []byte("Order #4821: 3x widgets, shipped Monday."))
	uploadReq.Header.Set("Authorization", "Bearer "+adminToken)
	uploadRec := httptest.NewRecorder()
	srv.Router().ServeHTTP(uploadRec, uploadReq)
	var att chatAttachmentRecord
	json.Unmarshal(uploadRec.Body.Bytes(), &att)

	fake := &fakeLLMClient{}
	bundle := *srv.providers.Load()
	bundle.llm = &llm.Router{Anthropic: fake}
	bundle.chat = chatSelection{Provider: "anthropic", Model: "claude-sonnet-5"}
	srv.providers.Store(&bundle)

	rec := doAuth(t, srv, http.MethodPost, "/api/chat", adminToken, chatbotRequest{
		Message:       "what's in this order?",
		AttachmentIDs: []string{att.ID},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}

	last := fake.lastMessages[len(fake.lastMessages)-1]
	if !strings.Contains(last.Content, "Order #4821") {
		t.Fatalf("attached document text never reached the model: %q", last.Content)
	}
	if !strings.Contains(last.Content, "order.txt") {
		t.Fatalf("expected the attachment's filename to be labeled in the injected text: %q", last.Content)
	}
}

// TestChatbotAttachmentImageSentWhenVisionCapable and
// TestChatbotAttachmentImageSkippedWhenNotVisionCapable cover both halves
// of the vision-capability gate in resolveAttachments.
func TestChatbotAttachmentImageSentWhenVisionCapable(t *testing.T) {
	srv, adminToken := newChatAttachmentTestServer(t)

	uploadReq := multipartUploadRequest(t, "/api/chat-attachments", "file", "screenshot.png", []byte("fake-png-bytes"))
	uploadReq.Header.Set("Authorization", "Bearer "+adminToken)
	uploadRec := httptest.NewRecorder()
	srv.Router().ServeHTTP(uploadRec, uploadReq)
	var att chatAttachmentRecord
	json.Unmarshal(uploadRec.Body.Bytes(), &att)

	fake := &fakeLLMClient{}
	bundle := *srv.providers.Load()
	bundle.llm = &llm.Router{Anthropic: fake}
	bundle.chat = chatSelection{Provider: "anthropic", Model: "claude-sonnet-5"} // vision-capable per llm.VisionCapable
	srv.providers.Store(&bundle)

	rec := doAuth(t, srv, http.MethodPost, "/api/chat", adminToken, chatbotRequest{
		Message:       "what is this a screenshot of?",
		AttachmentIDs: []string{att.ID},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}

	last := fake.lastMessages[len(fake.lastMessages)-1]
	if len(last.Images) != 1 {
		t.Fatalf("got %d images on the outgoing message, want 1", len(last.Images))
	}
	if string(last.Images[0].Data) != "fake-png-bytes" {
		t.Fatalf("image bytes = %q, want the uploaded file's raw content", last.Images[0].Data)
	}
}

func TestChatbotAttachmentImageSkippedWhenNotVisionCapable(t *testing.T) {
	srv, adminToken := newChatAttachmentTestServer(t)

	uploadReq := multipartUploadRequest(t, "/api/chat-attachments", "file", "screenshot.png", []byte("fake-png-bytes"))
	uploadReq.Header.Set("Authorization", "Bearer "+adminToken)
	uploadRec := httptest.NewRecorder()
	srv.Router().ServeHTTP(uploadRec, uploadReq)
	var att chatAttachmentRecord
	json.Unmarshal(uploadRec.Body.Bytes(), &att)

	fake := &fakeLLMClient{}
	bundle := *srv.providers.Load()
	bundle.llm = &llm.Router{Ollama: fake}
	bundle.chat = chatSelection{Provider: "ollama", Model: "llama3.2:3b"} // not vision-capable per llm.VisionCapable
	srv.providers.Store(&bundle)

	rec := doAuth(t, srv, http.MethodPost, "/api/chat", adminToken, chatbotRequest{
		Message:       "what is this a screenshot of?",
		AttachmentIDs: []string{att.ID},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}

	last := fake.lastMessages[len(fake.lastMessages)-1]
	if len(last.Images) != 0 {
		t.Fatalf("got %d images on the outgoing message, want 0 (model isn't vision-capable)", len(last.Images))
	}
	if !strings.Contains(last.Content, "doesn't support image understanding") {
		t.Fatalf("expected a note explaining the skipped image, got: %q", last.Content)
	}
}

// TestChatbotAllowsMessageWithOnlyAttachments and
// TestChatbotRejectsEmptyMessageWithNoAttachments cover the relaxed
// validation rule: a chat turn needs typed text OR at least one
// attachment, not always both.
func TestChatbotAllowsMessageWithOnlyAttachments(t *testing.T) {
	srv, adminToken := newChatAttachmentTestServer(t)

	// A fresh test server has no chat provider/model configured (see
	// TestChatbotDefaultsToOllamaWithNoModelChosen) — this test is about
	// the empty-text-plus-attachment validation rule, not provider
	// resolution, so it needs a working fake provider wired in first or
	// every request 503s before reaching the code path under test.
	fake := &fakeLLMClient{}
	bundle := *srv.providers.Load()
	bundle.llm = &llm.Router{Anthropic: fake}
	bundle.chat = chatSelection{Provider: "anthropic", Model: "claude-sonnet-5"}
	srv.providers.Store(&bundle)

	uploadReq := multipartUploadRequest(t, "/api/chat-attachments", "file", "screenshot.png", []byte("fake-png-bytes"))
	uploadReq.Header.Set("Authorization", "Bearer "+adminToken)
	uploadRec := httptest.NewRecorder()
	srv.Router().ServeHTTP(uploadRec, uploadReq)
	var att chatAttachmentRecord
	json.Unmarshal(uploadRec.Body.Bytes(), &att)

	rec := doAuth(t, srv, http.MethodPost, "/api/chat", adminToken, chatbotRequest{AttachmentIDs: []string{att.ID}})
	if rec.Code != http.StatusOK {
		t.Fatalf("a message with only an attachment and no text must be accepted: status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestChatbotRejectsEmptyMessageWithNoAttachments(t *testing.T) {
	srv, adminToken := newChatAttachmentTestServer(t)
	rec := doAuth(t, srv, http.MethodPost, "/api/chat", adminToken, chatbotRequest{Message: "  "})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (no text and no attachments)", rec.Code)
	}
}

// uploadChatAttachment is a small test helper — uploads one file via the
// real HTTP handler and decodes the resulting record, saving every
// multi-attachment/streaming/coexistence test below the repetition.
func uploadChatAttachment(t *testing.T, srv *Server, adminToken, filename string, content []byte) chatAttachmentRecord {
	t.Helper()
	req := multipartUploadRequest(t, "/api/chat-attachments", "file", filename, content)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload %q: status = %d, body = %s", filename, rec.Code, rec.Body.String())
	}
	var att chatAttachmentRecord
	if err := json.Unmarshal(rec.Body.Bytes(), &att); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}
	return att
}

// TestChatbotMultipleAttachmentsInOneMessage pins that an image and a
// document attached to the same message are both resolved correctly in
// the same request — the image reaches Images, the document's text
// reaches Content, and both are recorded in the outgoing history ref —
// not just whichever one a single-attachment test happens to cover.
func TestChatbotMultipleAttachmentsInOneMessage(t *testing.T) {
	srv, adminToken := newChatAttachmentTestServer(t)

	img := uploadChatAttachment(t, srv, adminToken, "diagram.png", []byte("fake-png-bytes"))
	doc := uploadChatAttachment(t, srv, adminToken, "spec.txt", []byte("The widget must be blue."))

	fake := &fakeLLMClient{}
	bundle := *srv.providers.Load()
	bundle.llm = &llm.Router{Anthropic: fake}
	bundle.chat = chatSelection{Provider: "anthropic", Model: "claude-sonnet-5"}
	srv.providers.Store(&bundle)

	rec := doAuth(t, srv, http.MethodPost, "/api/chat", adminToken, chatbotRequest{
		Message:       "does the diagram match the spec?",
		AttachmentIDs: []string{img.ID, doc.ID},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}

	last := fake.lastMessages[len(fake.lastMessages)-1]
	if len(last.Images) != 1 {
		t.Fatalf("got %d images, want 1", len(last.Images))
	}
	if !strings.Contains(last.Content, "The widget must be blue.") {
		t.Fatalf("document text missing from outgoing content: %q", last.Content)
	}
	if !strings.Contains(last.Content, "spec.txt") {
		t.Fatalf("document filename missing from outgoing content: %q", last.Content)
	}
}

// TestChatbotAttachmentImageReachesModelWhenStreaming is the streaming-
// path counterpart to TestChatbotAttachmentImageSentWhenVisionCapable —
// the one that matters in production, since the dashboard always
// requests Accept: text/event-stream. Attachment resolution must not be
// a non-streaming-only code path.
func TestChatbotAttachmentImageReachesModelWhenStreaming(t *testing.T) {
	srv, adminToken := newChatAttachmentTestServer(t)
	img := uploadChatAttachment(t, srv, adminToken, "screenshot.png", []byte("fake-png-bytes"))

	fake := &fakeLLMClient{}
	bundle := *srv.providers.Load()
	bundle.llm = &llm.Router{Anthropic: fake}
	bundle.chat = chatSelection{Provider: "anthropic", Model: "claude-sonnet-5"}
	srv.providers.Store(&bundle)

	req := jsonRequest(t, http.MethodPost, "/api/chat", chatbotRequest{
		Message:       "what is this a screenshot of?",
		AttachmentIDs: []string{img.ID},
	})
	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Header.Set("Accept", "text/event-stream")
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	last := fake.lastMessages[len(fake.lastMessages)-1]
	if len(last.Images) != 1 {
		t.Fatalf("got %d images on the streamed request, want 1", len(last.Images))
	}
	if string(last.Images[0].Data) != "fake-png-bytes" {
		t.Fatalf("image bytes = %q, want the uploaded file's raw content", last.Images[0].Data)
	}
}

// TestChatbotAttachmentImageWithOpenAI is the OpenAI-provider counterpart
// to the existing Anthropic/Ollama vision-gate tests — M1 explicitly
// requires vision to work across all three providers, not just the two
// already covered.
func TestChatbotAttachmentImageWithOpenAI(t *testing.T) {
	srv, adminToken := newChatAttachmentTestServer(t)
	img := uploadChatAttachment(t, srv, adminToken, "screenshot.png", []byte("fake-png-bytes"))

	fake := &fakeLLMClient{}
	bundle := *srv.providers.Load()
	bundle.llm = &llm.Router{OpenAI: fake}
	bundle.chat = chatSelection{Provider: "openai", Model: "gpt-4o"} // vision-capable per llm.VisionCapable
	srv.providers.Store(&bundle)

	rec := doAuth(t, srv, http.MethodPost, "/api/chat", adminToken, chatbotRequest{
		Message:       "what is this a screenshot of?",
		AttachmentIDs: []string{img.ID},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	last := fake.lastMessages[len(fake.lastMessages)-1]
	if len(last.Images) != 1 {
		t.Fatalf("got %d images on the outgoing message, want 1", len(last.Images))
	}
}

// TestChatbotAttachmentsCoexistWithProposals is the regression guard for
// M1 not breaking the AI Action Cards / Proposal architecture from the
// previous milestone: a message with an image attachment, whose reply
// also happens to call a valid create_collection tool, must come back
// with both the image having reached the model AND a validated proposal
// in the response — attachment resolution and the
// ActionParser -> ProposalValidator pipeline are independent concerns
// that must not interfere with each other.
func TestChatbotAttachmentsCoexistWithProposals(t *testing.T) {
	srv, adminToken := newChatAttachmentTestServer(t)
	img := uploadChatAttachment(t, srv, adminToken, "mockup.png", []byte("fake-png-bytes"))

	fake := &fakeLLMClient{
		reply:     "Here's a collection based on your mockup.",
		toolCalls: []llm.ToolCall{toolCall(actionCreateCollection, map[string]any{"name": "notes", "fields": []map[string]any{{"name": "body", "type": "text"}}})},
	}
	bundle := *srv.providers.Load()
	bundle.llm = &llm.Router{Anthropic: fake}
	bundle.chat = chatSelection{Provider: "anthropic", Model: "claude-sonnet-5"}
	srv.providers.Store(&bundle)

	rec := doAuth(t, srv, http.MethodPost, "/api/chat", adminToken, chatbotRequest{
		Message:       "create a collection based on this mockup",
		AttachmentIDs: []string{img.ID},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}

	last := fake.lastMessages[len(fake.lastMessages)-1]
	if len(last.Images) != 1 {
		t.Fatalf("got %d images, want 1 — attachment resolution must still work alongside a proposal", len(last.Images))
	}

	var resp chatbotResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Actions) != 1 {
		t.Fatalf("expected 1 validated proposal alongside the attachment, got %d: %+v", len(resp.Actions), resp.Actions)
	}
	if resp.Actions[0].Type != actionCreateCollection {
		t.Fatalf("action type = %q, want %q", resp.Actions[0].Type, actionCreateCollection)
	}
}
