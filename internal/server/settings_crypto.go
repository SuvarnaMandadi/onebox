package server

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
)

// settingsKey derives a stable AES-256 key from the server's JWT secret,
// so provider API keys can be encrypted at rest (per the anti-scope:
// "encrypt provider API keys at rest from day one") without requiring a
// second required secret from self-hosters — JWTSecret is already
// mandatory to set for production.
func settingsKey(jwtSecret string) [32]byte {
	return sha256.Sum256([]byte("onebox-settings-encryption:" + jwtSecret))
}

// encryptSetting returns a base64 string safe to store in the _settings
// table: AES-256-GCM, nonce prepended to ciphertext.
func encryptSetting(jwtSecret, plaintext string) (string, error) {
	key := settingsKey(jwtSecret)
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", fmt.Errorf("new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("new gcm: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}
	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

func decryptSetting(jwtSecret, encoded string) (string, error) {
	key := settingsKey(jwtSecret)
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", fmt.Errorf("new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("new gcm: %w", err)
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("decode base64: %w", err)
	}
	if len(data) < gcm.NonceSize() {
		return "", fmt.Errorf("ciphertext too short")
	}
	nonce, ciphertext := data[:gcm.NonceSize()], data[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt: %w", err)
	}
	return string(plaintext), nil
}
