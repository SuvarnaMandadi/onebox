package auth

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// SubjectType distinguishes end-user sessions from admin dashboard sessions,
// since they authorize against different tables and route groups.
type SubjectType string

const (
	SubjectUser  SubjectType = "user"
	SubjectAdmin SubjectType = "admin"
)

const defaultTTL = 7 * 24 * time.Hour

// Claims is the JWT payload for onebox sessions.
type Claims struct {
	Type SubjectType `json:"type"`
	jwt.RegisteredClaims
}

// IssueToken signs a session JWT for the given subject id and type.
func IssueToken(secret, subjectID string, subjectType SubjectType) (string, error) {
	now := time.Now()
	claims := Claims{
		Type: subjectType,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   subjectID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(defaultTTL)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

// ParseToken verifies a session JWT and returns its claims.
func ParseToken(secret, tokenString string) (*Claims, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(secret), nil
	})
	if err != nil {
		return nil, err
	}
	if !token.Valid {
		return nil, fmt.Errorf("invalid token")
	}
	return claims, nil
}
