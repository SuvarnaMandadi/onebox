package server

import (
	"os"
	"strings"
	"testing"
)

func TestExtractTextUnsupportedType(t *testing.T) {
	_, err := extractText("resume.exe", []byte("binary content"))
	if err == nil {
		t.Fatal("expected an error for an unsupported extension, got nil")
	}
	for _, ext := range supportedRAGExtensionsList {
		if !strings.Contains(err.Error(), ext) {
			t.Errorf("error message %q should list supported extension %q", err.Error(), ext)
		}
	}
}

func TestExtractTextTxtAndMd(t *testing.T) {
	text, err := extractText("notes.txt", []byte("hello from a text file"))
	if err != nil {
		t.Fatalf("extractText() error = %v", err)
	}
	if text != "hello from a text file" {
		t.Fatalf("text = %q, want unchanged content", text)
	}
}

// TestExtractDOCXText uses a real .docx (internal/server/testdata/sample.docx,
// generated with python-docx) containing a heading, a regular paragraph,
// a second heading, another paragraph, and a table — the exact shape a
// real user-uploaded resume or policy doc would have.
func TestExtractDOCXText(t *testing.T) {
	content, err := os.ReadFile("testdata/sample.docx")
	if err != nil {
		t.Fatalf("read testdata/sample.docx: %v", err)
	}

	text, err := extractText("sample.docx", content)
	if err != nil {
		t.Fatalf("extractText() error = %v", err)
	}

	// Heading text.
	if !strings.Contains(text, "OneBox Release Audit Test Document") {
		t.Errorf("missing top-level heading text, got: %q", text)
	}
	if !strings.Contains(text, "Refund Policy") {
		t.Errorf("missing second-level heading text, got: %q", text)
	}
	// Regular paragraph text.
	if !strings.Contains(text, "exercises DOCX ingestion end to end") {
		t.Errorf("missing paragraph text, got: %q", text)
	}
	if !strings.Contains(text, "full refund within") {
		t.Errorf("missing second paragraph text, got: %q", text)
	}
	// Table cell text (header row + data rows).
	for _, want := range []string{"Plan", "Monthly Price", "Starter", "$19", "Pro", "$49"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing table cell text %q, got: %q", want, text)
		}
	}
}

// TestExtractDOCXTextRejectsGarbage confirms a non-zip / non-docx file
// with a .docx extension fails cleanly rather than panicking.
func TestExtractDOCXTextRejectsGarbage(t *testing.T) {
	_, err := extractText("fake.docx", []byte("not a real docx file"))
	if err == nil {
		t.Fatal("expected an error for a garbage .docx, got nil")
	}
}
