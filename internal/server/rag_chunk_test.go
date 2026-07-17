package server

import (
	"strings"
	"testing"
)

func TestChunkText(t *testing.T) {
	tests := []struct {
		name      string
		text      string
		wantEmpty bool
		minChunks int
	}{
		{name: "empty text", text: "", wantEmpty: true},
		{name: "whitespace only", text: "   \n\t  ", wantEmpty: true},
		{name: "short text is one chunk", text: "hello world, this is a short document.", minChunks: 1},
		{name: "long text splits into multiple chunks", text: strings.Repeat("word ", 1000), minChunks: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chunks := chunkText(tt.text)
			if tt.wantEmpty {
				if len(chunks) != 0 {
					t.Fatalf("got %d chunks, want 0", len(chunks))
				}
				return
			}
			if len(chunks) < tt.minChunks {
				t.Fatalf("got %d chunks, want at least %d", len(chunks), tt.minChunks)
			}
			for i, c := range chunks {
				if strings.TrimSpace(c) == "" {
					t.Fatalf("chunk %d is empty/whitespace", i)
				}
			}
		})
	}
}

func TestChunkTextOverlaps(t *testing.T) {
	// Build text long enough to guarantee multiple chunks, then verify
	// consecutive chunks share trailing/leading words (the overlap).
	var words []string
	for i := 0; i < 1000; i++ {
		words = append(words, "word"+string(rune('a'+i%26)))
	}
	text := strings.Join(words, " ")

	chunks := chunkText(text)
	if len(chunks) < 2 {
		t.Fatalf("got %d chunks, want at least 2 to test overlap", len(chunks))
	}

	for i := 0; i < len(chunks)-1; i++ {
		a := strings.Fields(chunks[i])
		b := strings.Fields(chunks[i+1])
		lastOfA := a[len(a)-1]
		found := false
		for _, w := range b[:min(len(b), 20)] {
			if w == lastOfA {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("chunk %d and %d don't appear to overlap (last word of chunk %d = %q not found near start of chunk %d)", i, i+1, i, lastOfA, i+1)
		}
	}
}
