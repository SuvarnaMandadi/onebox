package server

import (
	"archive/zip"
	"bytes"
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

// TestExtractTextCSV pins CSV as a plain-text passthrough (like .txt/.md) —
// added for the chat attachment pipeline (chat_attachment_extensions in
// chat_attachments.go); RAG ingestion's own allowlist deliberately still
// doesn't include it (see TestExtractTextUnsupportedType).
func TestExtractTextCSV(t *testing.T) {
	text, err := extractText("data.csv", []byte("name,age\nAlice,30\n"))
	if err != nil {
		t.Fatalf("extractText() error = %v", err)
	}
	if text != "name,age\nAlice,30\n" {
		t.Fatalf("text = %q, want unchanged content", text)
	}
}

// buildTestXLSX assembles a minimal .xlsx zip archive from hand-written
// worksheet/sharedStrings XML — real-world structure (verified separately
// against a python-generated openpyxl workbook while writing
// extractXLSXText), without checking a binary fixture into the repo.
// [Content_Types].xml/workbook.xml are intentionally omitted: extractXLSXText
// only ever reads xl/sharedStrings.xml and xl/worksheets/sheet*.xml, so a
// real xlsx's other required parts aren't needed to exercise it.
func buildTestXLSX(t *testing.T, sharedStringsXML string, sheets map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	if sharedStringsXML != "" {
		w, err := zw.Create("xl/sharedStrings.xml")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(sharedStringsXML)); err != nil {
			t.Fatal(err)
		}
	}
	for name, content := range sheets {
		w, err := zw.Create("xl/worksheets/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

const testSharedStrings = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" count="3" uniqueCount="3">
<si><t>Name</t></si>
<si><t>Age</t></si>
<si><t>Bob</t></si>
</sst>`

const testSheet1 = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">
<sheetData>
<row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1" t="s"><v>1</v></c></row>
<row r="2"><c r="A2" t="s"><v>2</v></c><c r="B2"><v>42</v></c></row>
<row r="3"><c r="A3" t="inlineStr"><is><t>Inline text</t></is></c></row>
</sheetData>
</worksheet>`

// TestExtractXLSXText covers the three cell shapes a real workbook mixes:
// shared-string references (t="s", the common case for any text cell),
// literal numbers (no t attribute), and inline strings (t="inlineStr").
func TestExtractXLSXText(t *testing.T) {
	content := buildTestXLSX(t, testSharedStrings, map[string]string{"sheet1.xml": testSheet1})

	text, err := extractText("data.xlsx", content)
	if err != nil {
		t.Fatalf("extractText() error = %v", err)
	}
	if !strings.Contains(text, "Name\tAge") {
		t.Errorf("missing shared-string header row, got: %q", text)
	}
	if !strings.Contains(text, "Bob\t42") {
		t.Errorf("missing shared-string + numeric row, got: %q", text)
	}
	if !strings.Contains(text, "Inline text") {
		t.Errorf("missing inline-string cell, got: %q", text)
	}
	// A single sheet shouldn't get a "Sheet 1:" label — that's only added
	// once there's more than one to disambiguate.
	if strings.Contains(text, "Sheet 1:") {
		t.Errorf("single-sheet workbook must not be labeled, got: %q", text)
	}
}

// TestExtractXLSXTextMultipleSheets confirms sheets are read in filename
// order and labeled positionally.
func TestExtractXLSXTextMultipleSheets(t *testing.T) {
	sheet2 := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">
<sheetData>
<row r="1"><c r="A1" t="inlineStr"><is><t>Second sheet cell</t></is></c></row>
</sheetData>
</worksheet>`
	content := buildTestXLSX(t, testSharedStrings, map[string]string{"sheet1.xml": testSheet1, "sheet2.xml": sheet2})

	text, err := extractText("data.xlsx", content)
	if err != nil {
		t.Fatalf("extractText() error = %v", err)
	}
	if !strings.Contains(text, "Sheet 1:") || !strings.Contains(text, "Sheet 2:") {
		t.Fatalf("expected both sheets labeled, got: %q", text)
	}
	if strings.Index(text, "Sheet 1:") > strings.Index(text, "Sheet 2:") {
		t.Fatalf("sheets out of order, got: %q", text)
	}
	if !strings.Contains(text, "Second sheet cell") {
		t.Errorf("missing second sheet's content, got: %q", text)
	}
}

// TestExtractXLSXTextRejectsGarbage confirms a non-zip file with an .xlsx
// extension fails cleanly rather than panicking, and that a zip with no
// worksheet parts at all (structurally not a workbook) also errors instead
// of silently returning empty text.
func TestExtractXLSXTextRejectsGarbage(t *testing.T) {
	if _, err := extractText("fake.xlsx", []byte("not a real xlsx file")); err == nil {
		t.Fatal("expected an error for a garbage .xlsx, got nil")
	}
	empty := buildTestXLSX(t, "", nil)
	if _, err := extractText("empty.xlsx", empty); err == nil {
		t.Fatal("expected an error for an xlsx with no worksheets, got nil")
	}
}
