package server

import (
	"context"
	"strings"
	"testing"
)

// TestDescribeWorkspaceEmptyPage is the base case: no page means no
// workspace section at all, not an empty-but-present one — callers
// (answerChatbotQuestion) rely on "" being safe to append with no extra
// separators.
func TestDescribeWorkspaceEmptyPage(t *testing.T) {
	srv, _ := newTestServer(t)
	if got := srv.describeWorkspace(context.Background(), workspaceContext{}); got != "" {
		t.Fatalf("describeWorkspace(zero value) = %q, want \"\"", got)
	}
}

// TestDescribeWorkspaceCollectionsPage is the core "workspace awareness"
// regression test: with a collection open, its live schema — not just the
// generic collections summary — must be in the prompt text, since that's
// what lets "add an email field" resolve without the admin re-typing
// which collection they mean.
func TestDescribeWorkspaceCollectionsPage(t *testing.T) {
	srv, _ := newTestServer(t)
	if _, err := createCollection(context.Background(), srv.db, "messages", Schema{Fields: []Field{
		{Name: "sender", Type: FieldText, Required: true},
	}}, Rules{}); err != nil {
		t.Fatalf("createCollection: %v", err)
	}

	got := srv.describeWorkspace(context.Background(), workspaceContext{Page: "records", Collection: "messages"})
	if !strings.Contains(got, `collection "messages"`) {
		t.Fatalf("missing current collection name: %s", got)
	}
	if !strings.Contains(got, "sender: text (required)") {
		t.Fatalf("missing current collection's field detail: %s", got)
	}
}

// TestDescribeWorkspaceCollectionsPageUnknownCollection covers the "the
// admin just deleted what they had open" race — it must degrade to a
// clear note, never panic or surface an internal_error for what's meant
// to be best-effort context.
func TestDescribeWorkspaceCollectionsPageUnknownCollection(t *testing.T) {
	srv, _ := newTestServer(t)
	got := srv.describeWorkspace(context.Background(), workspaceContext{Page: "records", Collection: "ghost"})
	if !strings.Contains(got, `"ghost"`) || !strings.Contains(got, "couldn't be loaded") {
		t.Fatalf("expected a graceful not-found note, got: %s", got)
	}
}

// TestDescribeWorkspaceRecordDetail covers the specific record open in the
// record editor modal — it must appear in the workspace section, encoded
// as its actual field values.
func TestDescribeWorkspaceRecordDetail(t *testing.T) {
	srv, _ := newTestServer(t)
	c, err := createCollection(context.Background(), srv.db, "messages", Schema{Fields: []Field{
		{Name: "sender", Type: FieldText},
	}}, Rules{})
	if err != nil {
		t.Fatalf("createCollection: %v", err)
	}
	rec, err := createRecord(context.Background(), srv.db, c, map[string]any{"sender": "alice"}, "")
	if err != nil {
		t.Fatalf("createRecord: %v", err)
	}

	got := srv.describeWorkspace(context.Background(), workspaceContext{Page: "records", Collection: "messages", RecordID: rec["id"].(string)})
	if !strings.Contains(got, "alice") {
		t.Fatalf("missing open record's content: %s", got)
	}
}

// TestDescribeWorkspaceRAGPage covers the RAG page: ingested document
// status must be visible so "why isn't this document answering" can be
// answered from the document's actual status instead of guessing.
func TestDescribeWorkspaceRAGPage(t *testing.T) {
	srv, _ := newTestServer(t)
	src, err := createRAGSource(context.Background(), srv.db, "", "file-1", "handbook.pdf")
	if err != nil {
		t.Fatalf("createRAGSource: %v", err)
	}
	if err := setRAGSourceStatus(context.Background(), srv.db, src.ID, "error", "embedding provider not configured", 0); err != nil {
		t.Fatalf("setRAGSourceStatus: %v", err)
	}

	got := srv.describeWorkspace(context.Background(), workspaceContext{Page: "rag"})
	if !strings.Contains(got, "handbook.pdf") || !strings.Contains(got, "error") || !strings.Contains(got, "embedding provider not configured") {
		t.Fatalf("missing RAG source status detail: %s", got)
	}
}

// TestDescribeWorkspaceSettingsPage covers the Settings page: the current
// Chat/Embedding provider configuration must be visible so provider
// questions don't need the admin to paste their own settings back.
func TestDescribeWorkspaceSettingsPage(t *testing.T) {
	srv, _ := newTestServer(t)
	got := srv.describeWorkspace(context.Background(), workspaceContext{Page: "settings"})
	if !strings.Contains(got, "Chat provider: ollama") {
		t.Fatalf("missing chat provider detail: %s", got)
	}
}
