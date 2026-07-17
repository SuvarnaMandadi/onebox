package auth

import "testing"

func TestIssueAndParseToken(t *testing.T) {
	tests := []struct {
		name        string
		secret      string
		subjectID   string
		subjectType SubjectType
	}{
		{name: "user token", secret: "s3cret", subjectID: "user-123", subjectType: SubjectUser},
		{name: "admin token", secret: "s3cret", subjectID: "admin-456", subjectType: SubjectAdmin},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token, err := IssueToken(tt.secret, tt.subjectID, tt.subjectType)
			if err != nil {
				t.Fatalf("IssueToken() error = %v", err)
			}

			claims, err := ParseToken(tt.secret, token)
			if err != nil {
				t.Fatalf("ParseToken() error = %v", err)
			}
			if claims.Subject != tt.subjectID {
				t.Errorf("Subject = %q, want %q", claims.Subject, tt.subjectID)
			}
			if claims.Type != tt.subjectType {
				t.Errorf("Type = %q, want %q", claims.Type, tt.subjectType)
			}
		})
	}
}

func TestParseTokenRejectsWrongSecret(t *testing.T) {
	token, err := IssueToken("right-secret", "user-1", SubjectUser)
	if err != nil {
		t.Fatalf("IssueToken() error = %v", err)
	}

	if _, err := ParseToken("wrong-secret", token); err == nil {
		t.Fatal("expected error parsing token with wrong secret, got nil")
	}
}

func TestParseTokenRejectsGarbage(t *testing.T) {
	if _, err := ParseToken("s3cret", "not-a-jwt"); err == nil {
		t.Fatal("expected error parsing garbage token, got nil")
	}
}
