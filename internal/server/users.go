package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// user is a row from _users.
type user struct {
	ID           string `json:"id"`
	Email        string `json:"email"`
	PasswordHash string `json:"-"`
	Verified     bool   `json:"verified"`
	Created      string `json:"created"`
	Updated      string `json:"updated"`
}

var errEmailTaken = errors.New("email already registered")

func createUser(ctx context.Context, sqlDB *sql.DB, email, passwordHash string) (*user, error) {
	id := uuid.NewString()

	_, err := sqlDB.ExecContext(ctx,
		`INSERT INTO _users (id, email, password_hash) VALUES (?, ?, ?)`,
		id, email, passwordHash,
	)
	if err != nil {
		if isUniqueConstraintErr(err) {
			return nil, errEmailTaken
		}
		return nil, fmt.Errorf("insert user: %w", err)
	}

	return getUserByID(ctx, sqlDB, id)
}

func getUserByEmail(ctx context.Context, sqlDB *sql.DB, email string) (*user, error) {
	row := sqlDB.QueryRowContext(ctx,
		`SELECT id, email, password_hash, verified, created, updated FROM _users WHERE email = ?`,
		email,
	)
	return scanUser(row)
}

func getUserByID(ctx context.Context, sqlDB *sql.DB, id string) (*user, error) {
	row := sqlDB.QueryRowContext(ctx,
		`SELECT id, email, password_hash, verified, created, updated FROM _users WHERE id = ?`,
		id,
	)
	return scanUser(row)
}

func scanUser(row *sql.Row) (*user, error) {
	var u user
	var verified int
	if err := row.Scan(&u.ID, &u.Email, &u.PasswordHash, &verified, &u.Created, &u.Updated); err != nil {
		return nil, err
	}
	u.Verified = verified != 0
	return &u, nil
}

// isUniqueConstraintErr reports whether err came from a UNIQUE constraint
// violation, e.g. a duplicate email on insert.
func isUniqueConstraintErr(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}
