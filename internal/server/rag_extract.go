package server

import (
	"bytes"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/ledongthuc/pdf"
)

var supportedRAGExtensions = map[string]bool{".pdf": true, ".txt": true, ".md": true}

// extractText pulls plain text out of an uploaded document. Only
// PDF/TXT/MD are supported in v0.1, per the roadmap.
func extractText(filename string, content []byte) (string, error) {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".txt", ".md":
		return string(content), nil
	case ".pdf":
		return extractPDFText(content)
	default:
		return "", fmt.Errorf("unsupported file type %q (supported: .pdf, .txt, .md)", ext)
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
