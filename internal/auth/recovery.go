package auth

import (
	"crypto/rand"
	"math/big"
	"strings"
)

// RecoveryPhraseWords is the number of words in a generated recovery
// phrase — matches BIP39's convention (12 words) without claiming
// wordlist compatibility with it.
const RecoveryPhraseWords = 12

// GenerateRecoveryPhrase returns a space-separated phrase of
// RecoveryPhraseWords words drawn independently and uniformly at random
// from Wordlist (repeats are possible, same as BIP39 — each position is
// an independent draw, so excluding repeats would only ever reduce
// entropy, never increase it).
func GenerateRecoveryPhrase() (string, error) {
	words := make([]string, RecoveryPhraseWords)
	max := big.NewInt(int64(len(Wordlist)))
	for i := range words {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		words[i] = Wordlist[n.Int64()]
	}
	return strings.Join(words, " "), nil
}

// NormalizeRecoveryPhrase lowercases, trims, and collapses whitespace so a
// phrase typed with extra spaces or mixed case still verifies correctly.
func NormalizeRecoveryPhrase(phrase string) string {
	fields := strings.Fields(strings.ToLower(phrase))
	return strings.Join(fields, " ")
}
