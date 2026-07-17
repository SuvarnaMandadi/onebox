package server

import "strings"

// Chunk sizes are approximated in characters (~4 chars/token for English
// text) rather than a real tokenizer, keeping the pipeline dependency-free
// per the roadmap's "~500 tokens with overlap" guidance.
const (
	chunkSizeChars    = 2000 // ~500 tokens
	chunkOverlapChars = 200  // ~50 tokens
)

// chunkText splits text into overlapping, word-boundary-safe chunks.
func chunkText(text string) []string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}

	var chunks []string
	start := 0
	for start < len(words) {
		end, length := start, 0
		for end < len(words) && length < chunkSizeChars {
			length += len(words[end]) + 1
			end++
		}
		chunks = append(chunks, strings.Join(words[start:end], " "))
		if end >= len(words) {
			break
		}

		overlapStart, overlapLen := end, 0
		for overlapStart > start && overlapLen < chunkOverlapChars {
			overlapStart--
			overlapLen += len(words[overlapStart]) + 1
		}
		if overlapStart <= start {
			overlapStart = end // safety: guarantee forward progress
		}
		start = overlapStart
	}
	return chunks
}
