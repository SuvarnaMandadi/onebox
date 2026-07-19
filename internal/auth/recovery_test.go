package auth

import (
	"strings"
	"testing"
)

func TestGenerateRecoveryPhrase(t *testing.T) {
	inWordlist := make(map[string]bool, len(Wordlist))
	for _, w := range Wordlist {
		inWordlist[w] = true
	}

	seenPhrases := make(map[string]bool)
	for i := 0; i < 20; i++ {
		phrase, err := GenerateRecoveryPhrase()
		if err != nil {
			t.Fatalf("generate: %v", err)
		}
		words := strings.Fields(phrase)
		if len(words) != RecoveryPhraseWords {
			t.Fatalf("got %d words, want %d: %q", len(words), RecoveryPhraseWords, phrase)
		}
		for _, w := range words {
			if !inWordlist[w] {
				t.Fatalf("word %q is not in Wordlist", w)
			}
		}
		if seenPhrases[phrase] {
			t.Fatalf("generated the same phrase twice across %d attempts: %q", i+1, phrase)
		}
		seenPhrases[phrase] = true
	}
}

func TestNormalizeRecoveryPhrase(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"  Apple   Banana\tCherry ", "apple banana cherry"},
		{"APPLE BANANA", "apple banana"},
		{"apple banana", "apple banana"},
	}
	for _, tt := range tests {
		if got := NormalizeRecoveryPhrase(tt.in); got != tt.want {
			t.Errorf("NormalizeRecoveryPhrase(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestRecoveryPhraseHashing(t *testing.T) {
	phrase, err := GenerateRecoveryPhrase()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	hash, err := HashPassword(NormalizeRecoveryPhrase(phrase))
	if err != nil {
		t.Fatalf("hash: %v", err)
	}

	ok, err := VerifyPassword(NormalizeRecoveryPhrase(phrase), hash)
	if err != nil || !ok {
		t.Fatalf("expected the same phrase to verify, got ok=%v err=%v", ok, err)
	}

	ok, err = VerifyPassword(NormalizeRecoveryPhrase(strings.ToUpper(phrase)), hash)
	if err != nil || !ok {
		t.Fatalf("expected a differently-cased-then-normalized phrase to verify, got ok=%v err=%v", ok, err)
	}

	otherPhrase, err := GenerateRecoveryPhrase()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	ok, err = VerifyPassword(NormalizeRecoveryPhrase(otherPhrase), hash)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if ok {
		t.Fatalf("a different phrase must not verify against this hash")
	}
}
