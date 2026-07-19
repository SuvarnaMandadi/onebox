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
	ID                 string `json:"id"`
	Email              string `json:"email"`
	PasswordHash       string `json:"-"`
	Verified           bool   `json:"verified"`
	FirstName          string `json:"first_name"`
	LastName           string `json:"last_name"`
	Phone              string `json:"phone"`
	AvatarFileID       string `json:"avatar_file_id,omitempty"`
	RecoveryPhraseHash string `json:"-"`
	Created            string `json:"created"`
	Updated            string `json:"updated"`
}

var errEmailTaken = errors.New("email already registered")

const userColumns = "id, email, password_hash, verified, first_name, last_name, phone, avatar_file_id, recovery_phrase_hash, created, updated"

func createUser(ctx context.Context, sqlDB *sql.DB, email, passwordHash, firstName, lastName, recoveryPhraseHash string) (*user, error) {
	id := uuid.NewString()

	_, err := sqlDB.ExecContext(ctx,
		`INSERT INTO _users (id, email, password_hash, first_name, last_name, recovery_phrase_hash) VALUES (?, ?, ?, ?, ?, ?)`,
		id, email, passwordHash, firstName, lastName, recoveryPhraseHash,
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
		`SELECT `+userColumns+` FROM _users WHERE email = ?`,
		email,
	)
	return scanUser(row)
}

func getUserByID(ctx context.Context, sqlDB *sql.DB, id string) (*user, error) {
	row := sqlDB.QueryRowContext(ctx,
		`SELECT `+userColumns+` FROM _users WHERE id = ?`,
		id,
	)
	return scanUser(row)
}

func scanUser(row *sql.Row) (*user, error) {
	var u user
	var verified int
	var avatarFileID, recoveryPhraseHash sql.NullString
	if err := row.Scan(&u.ID, &u.Email, &u.PasswordHash, &verified, &u.FirstName, &u.LastName, &u.Phone, &avatarFileID, &recoveryPhraseHash, &u.Created, &u.Updated); err != nil {
		return nil, err
	}
	u.Verified = verified != 0
	u.AvatarFileID = avatarFileID.String
	u.RecoveryPhraseHash = recoveryPhraseHash.String
	return &u, nil
}

// updateUserProfile updates the editable profile fields on a _users row.
// Email is re-lowercased/trimmed by the caller before this is invoked, and a
// change to an email already in use surfaces as errEmailTaken.
func updateUserProfile(ctx context.Context, sqlDB *sql.DB, id, email, firstName, lastName, phone string) (*user, error) {
	_, err := sqlDB.ExecContext(ctx,
		`UPDATE _users SET email = ?, first_name = ?, last_name = ?, phone = ?, updated = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ?`,
		email, firstName, lastName, phone, id,
	)
	if err != nil {
		if isUniqueConstraintErr(err) {
			return nil, errEmailTaken
		}
		return nil, fmt.Errorf("update user profile: %w", err)
	}
	return getUserByID(ctx, sqlDB, id)
}

func updateUserAvatar(ctx context.Context, sqlDB *sql.DB, id, avatarFileID string) (*user, error) {
	_, err := sqlDB.ExecContext(ctx,
		`UPDATE _users SET avatar_file_id = ?, updated = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ?`,
		avatarFileID, id,
	)
	if err != nil {
		return nil, fmt.Errorf("update user avatar: %w", err)
	}
	return getUserByID(ctx, sqlDB, id)
}

func updateUserPasswordHash(ctx context.Context, sqlDB *sql.DB, id, passwordHash string) error {
	_, err := sqlDB.ExecContext(ctx,
		`UPDATE _users SET password_hash = ?, updated = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ?`,
		passwordHash, id,
	)
	if err != nil {
		return fmt.Errorf("update user password: %w", err)
	}
	return nil
}

func updateUserRecoveryPhraseHash(ctx context.Context, sqlDB *sql.DB, id, recoveryPhraseHash string) error {
	_, err := sqlDB.ExecContext(ctx,
		`UPDATE _users SET recovery_phrase_hash = ?, updated = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ?`,
		recoveryPhraseHash, id,
	)
	if err != nil {
		return fmt.Errorf("update user recovery phrase: %w", err)
	}
	return nil
}

// isUniqueConstraintErr reports whether err came from a UNIQUE constraint
// violation, e.g. a duplicate email on insert.
func isUniqueConstraintErr(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}
