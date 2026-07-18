package server

import "testing"

func TestEncryptDecryptSettingRoundTrip(t *testing.T) {
	tests := []struct {
		name      string
		secret    string
		plaintext string
	}{
		{name: "typical api key", secret: "jwt-secret", plaintext: "sk-ant-api03-abc123"},
		{name: "empty plaintext", secret: "jwt-secret", plaintext: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encrypted, err := encryptSetting(tt.secret, tt.plaintext)
			if err != nil {
				t.Fatalf("encryptSetting() error = %v", err)
			}
			if encrypted == tt.plaintext {
				t.Fatal("encrypted value must not equal plaintext")
			}
			decrypted, err := decryptSetting(tt.secret, encrypted)
			if err != nil {
				t.Fatalf("decryptSetting() error = %v", err)
			}
			if decrypted != tt.plaintext {
				t.Fatalf("decrypted = %q, want %q", decrypted, tt.plaintext)
			}
		})
	}
}

func TestDecryptSettingWrongSecretFails(t *testing.T) {
	encrypted, err := encryptSetting("right-secret", "sensitive-value")
	if err != nil {
		t.Fatalf("encryptSetting() error = %v", err)
	}
	if _, err := decryptSetting("wrong-secret", encrypted); err == nil {
		t.Fatal("expected error decrypting with the wrong secret, got nil")
	}
}
