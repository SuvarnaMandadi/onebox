package auth

import "testing"

// TestWordlistIsClean guards against transcription mistakes in the
// hand-written word list: duplicates silently reduce entropy per word
// (and would make a "duplicate word" bug possible in the phrase itself),
// and anything too short/malformed makes phrases harder to read back
// correctly.
func TestWordlistIsClean(t *testing.T) {
	seen := make(map[string]int, len(Wordlist))
	for i, w := range Wordlist {
		if w == "" {
			t.Fatalf("empty word at index %d", i)
		}
		if len(w) < 3 {
			t.Errorf("word %q at index %d is shorter than 3 letters", w, i)
		}
		for _, r := range w {
			if r < 'a' || r > 'z' {
				t.Errorf("word %q at index %d contains a non-lowercase-ASCII character", w, i)
				break
			}
		}
		if prev, ok := seen[w]; ok {
			t.Errorf("duplicate word %q at indices %d and %d", w, prev, i)
		}
		seen[w] = i
	}
}

func TestWordlistEntropy(t *testing.T) {
	const phraseWords = 12
	const minBits = 100.0

	n := len(Wordlist)
	if n < 256 {
		t.Fatalf("wordlist has only %d words — too small for a strong recovery phrase", n)
	}

	// bits = phraseWords * log2(n), computed without floating-point log to
	// keep this test dependency-free: find the largest power of two <= n.
	bitsPerWord := 0
	for (1 << (bitsPerWord + 1)) <= n {
		bitsPerWord++
	}
	totalBits := float64(phraseWords * bitsPerWord)
	if totalBits < minBits {
		t.Fatalf("wordlist of %d words gives only ~%d bits for a %d-word phrase, want >= %v", n, int(totalBits), phraseWords, minBits)
	}
}
