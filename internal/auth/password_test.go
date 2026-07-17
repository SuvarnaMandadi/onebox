package auth

import "testing"

func TestHashAndVerifyPassword(t *testing.T) {
	tests := []struct {
		name     string
		password string
		attempt  string
		want     bool
	}{
		{name: "correct password", password: "correct-horse-battery-staple", attempt: "correct-horse-battery-staple", want: true},
		{name: "wrong password", password: "correct-horse-battery-staple", attempt: "wrong-password", want: false},
		{name: "empty attempt", password: "correct-horse-battery-staple", attempt: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hash, err := HashPassword(tt.password)
			if err != nil {
				t.Fatalf("HashPassword() error = %v", err)
			}

			got, err := VerifyPassword(tt.attempt, hash)
			if err != nil {
				t.Fatalf("VerifyPassword() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("VerifyPassword() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHashPasswordProducesUniqueSalts(t *testing.T) {
	h1, err := HashPassword("same-password")
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	h2, err := HashPassword("same-password")
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	if h1 == h2 {
		t.Fatal("expected different hashes for same password due to random salt")
	}
}
