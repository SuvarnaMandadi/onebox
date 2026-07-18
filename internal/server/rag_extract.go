package server

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/ledongthuc/pdf"
)

var supportedRAGExtensions = map[string]bool{".pdf": true, ".txt": true, ".md": true, ".docx": true}

// supportedRAGExtensionsList is supportedRAGExtensions in a stable,
// human-readable order, for error messages.
var supportedRAGExtensionsList = []string{".pdf", ".txt", ".md", ".docx"}

// extractText pulls plain text out of an uploaded document. PDF/TXT/MD/DOCX
// are supported in v0.1, per the roadmap.
func extractText(filename string, content []byte) (string, error) {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".txt", ".md":
		return string(content), nil
	case ".pdf":
		return extractPDFText(content)
	case ".docx":
		return extractDOCXText(content)
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
