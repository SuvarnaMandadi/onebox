package server

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/ledongthuc/pdf"
)

var supportedRAGExtensions = map[string]bool{".pdf": true, ".txt": true, ".md": true, ".docx": true}

// supportedRAGExtensionsList is supportedRAGExtensions in a stable,
// human-readable order, for error messages.
var supportedRAGExtensionsList = []string{".pdf", ".txt", ".md", ".docx"}

// extractText pulls plain text out of an uploaded document. PDF/TXT/MD/DOCX
// are supported in v0.1, per the roadmap; CSV/XLSX were added for the chat
// attachment pipeline (see chatAttachmentExtensions in chat_attachments.go)
// — RAG ingestion's own allowlist (supportedRAGExtensions, just above)
// deliberately still only accepts the original four, so extending this
// switch doesn't silently widen what RAG document ingestion accepts too;
// both callers validate against their own allowlist before ever reaching
// here.
func extractText(filename string, content []byte) (string, error) {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".txt", ".md", ".csv":
		return string(content), nil
	case ".pdf":
		return extractPDFText(content)
	case ".docx":
		return extractDOCXText(content)
	case ".xlsx":
		return extractXLSXText(content)
	default:
		return "", fmt.Errorf("unsupported file type %q (supported: %s)", ext, strings.Join(supportedRAGExtensionsList, ", "))
	}
}

func extractPDFText(content []byte) (string, error) {
	r, err := pdf.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return "", fmt.Errorf("open pdf: %w", err)
	}
	textReader, err := r.GetPlainText()
	if err != nil {
		return "", fmt.Errorf("extract pdf text: %w", err)
	}
	text, err := io.ReadAll(textReader)
	if err != nil {
		return "", fmt.Errorf("read pdf text: %w", err)
	}
	return string(text), nil
}

// extractDOCXText pulls plain text out of a .docx file using only the
// standard library: a .docx is a zip archive, and its body text lives in
// word/document.xml as a sequence of <w:p> paragraphs containing <w:t>
// text runs (headings are just paragraphs with a different style — their
// text is <w:t> like any other, so they're captured the same way). Table
// cells are themselves paragraphs, so walking the token stream and
// breaking on </w:p> and </w:tr> naturally captures table text too,
// without needing separate table-structure handling.
func extractDOCXText(content []byte) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return "", fmt.Errorf("open docx as zip: %w", err)
	}

	var docFile *zip.File
	for _, f := range zr.File {
		if f.Name == "word/document.xml" {
			docFile = f
			break
		}
	}
	if docFile == nil {
		return "", fmt.Errorf("word/document.xml not found — not a valid .docx")
	}

	rc, err := docFile.Open()
	if err != nil {
		return "", fmt.Errorf("open document.xml: %w", err)
	}
	defer rc.Close()

	decoder := xml.NewDecoder(rc)
	var buf strings.Builder
	inText := false

	for {
		tok, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("parse document.xml: %w", err)
		}

		switch el := tok.(type) {
		case xml.StartElement:
			if el.Name.Local == "t" {
				inText = true
			}
		case xml.CharData:
			if inText {
				buf.Write(el)
			}
		case xml.EndElement:
			switch el.Name.Local {
			case "t":
				inText = false
			case "p", "tr":
				buf.WriteString("\n")
			}
		}
	}

	return buf.String(), nil
}

// extractXLSXText pulls plain text out of an .xlsx workbook using only the
// standard library, in the same spirit as extractDOCXText: an .xlsx is a
// zip archive holding one XML file per worksheet
// (xl/worksheets/sheetN.xml) plus a shared string table
// (xl/sharedStrings.xml) that most text cells reference by index rather
// than embedding their text directly. Sheets are read in filename order
// and labeled positionally ("Sheet 1", "Sheet 2", ...) — resolving each
// sheet's real display name would also mean parsing xl/workbook.xml and
// its relationship file, more machinery than this needs just to make a
// spreadsheet readable as chat context. Cell values are tab-separated
// within a row, rows newline-separated; merged/empty cells aren't
// reconstructed, so columns can drift on sheets with sparse data — an
// accepted limitation for the same reason.
func extractXLSXText(content []byte) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return "", fmt.Errorf("open xlsx as zip: %w", err)
	}

	shared, err := readXLSXSharedStrings(zr)
	if err != nil {
		return "", err
	}

	var sheetFiles []*zip.File
	for _, f := range zr.File {
		if strings.HasPrefix(f.Name, "xl/worksheets/sheet") && strings.HasSuffix(f.Name, ".xml") {
			sheetFiles = append(sheetFiles, f)
		}
	}
	if len(sheetFiles) == 0 {
		return "", fmt.Errorf("no worksheets found — not a valid .xlsx")
	}
	sort.Slice(sheetFiles, func(i, j int) bool { return sheetFiles[i].Name < sheetFiles[j].Name })

	var out strings.Builder
	for i, f := range sheetFiles {
		text, err := extractXLSXSheetText(f, shared)
		if err != nil {
			return "", err
		}
		if len(sheetFiles) > 1 {
			fmt.Fprintf(&out, "Sheet %d:\n", i+1)
		}
		out.WriteString(text)
		out.WriteString("\n")
	}
	return out.String(), nil
}

// readXLSXSharedStrings parses xl/sharedStrings.xml into an index-ordered
// slice, so extractXLSXSheetText can resolve a <c t="s"><v>N</v></c> cell
// to shared[N]. A workbook with no text cells at all (numbers only) may
// have no shared-strings part; that's not an error, just an empty table.
func readXLSXSharedStrings(zr *zip.Reader) ([]string, error) {
	var sstFile *zip.File
	for _, f := range zr.File {
		if f.Name == "xl/sharedStrings.xml" {
			sstFile = f
			break
		}
	}
	if sstFile == nil {
		return nil, nil
	}
	rc, err := sstFile.Open()
	if err != nil {
		return nil, fmt.Errorf("open sharedStrings.xml: %w", err)
	}
	defer rc.Close()

	// <si> ("string item") holds either a direct <t> or one or more <r>
	// ("rich text run") elements each with their own <t> — a cell styled
	// with mixed formatting (e.g. part bold) serializes as multiple runs
	// that need concatenating back into one string.
	var sst struct {
		SI []struct {
			T string `xml:"t"`
			R []struct {
				T string `xml:"t"`
			} `xml:"r"`
		} `xml:"si"`
	}
	if err := xml.NewDecoder(rc).Decode(&sst); err != nil {
		return nil, fmt.Errorf("parse sharedStrings.xml: %w", err)
	}

	out := make([]string, len(sst.SI))
	for i, si := range sst.SI {
		if si.T != "" || len(si.R) == 0 {
			out[i] = si.T
			continue
		}
		var b strings.Builder
		for _, r := range si.R {
			b.WriteString(r.T)
		}
		out[i] = b.String()
	}
	return out, nil
}

// extractXLSXSheetText renders one worksheet XML part as tab/newline text.
// Cell type "s" is a shared-string index, "inlineStr" carries its own text
// directly (<is><t>...</t></is>), and anything else (absent, "n", "str",
// "b") is a number/formula-result/boolean already in its literal form in
// <v> — safe to use as-is.
func extractXLSXSheetText(f *zip.File, shared []string) (string, error) {
	rc, err := f.Open()
	if err != nil {
		return "", fmt.Errorf("open %s: %w", f.Name, err)
	}
	defer rc.Close()

	var sheet struct {
		SheetData struct {
			Row []struct {
				C []struct {
					T  string `xml:"t,attr"`
					V  string `xml:"v"`
					Is struct {
						T string `xml:"t"`
					} `xml:"is"`
				} `xml:"c"`
			} `xml:"row"`
		} `xml:"sheetData"`
	}
	if err := xml.NewDecoder(rc).Decode(&sheet); err != nil {
		return "", fmt.Errorf("parse %s: %w", f.Name, err)
	}

	var b strings.Builder
	for _, row := range sheet.SheetData.Row {
		cells := make([]string, len(row.C))
		for i, c := range row.C {
			switch c.T {
			case "s":
				if idx, err := strconv.Atoi(c.V); err == nil && idx >= 0 && idx < len(shared) {
					cells[i] = shared[idx]
				}
			case "inlineStr":
				cells[i] = c.Is.T
			default:
				cells[i] = c.V
			}
		}
		b.WriteString(strings.Join(cells, "\t"))
		b.WriteString("\n")
	}
	return b.String(), nil
}
