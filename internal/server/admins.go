package server

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
)

type admin struct {
	ID           string `json:"id"`
	Email        string `json:"email"`
	PasswordHash string `json:"-"`
	Created      string `json:"created"`
	Updated      string `json:"updated"`
}

func countAdmins(ctx context.Context, sqlDB *sql.DB) (int, error) {
	var count int
	err := sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM _admins`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count admins: %w", err)
	}
	return count, nil
}

func createAdmin(ctx context.Context, sqlDB *sql.DB, email, passwordHash string) (*admin, error) {
	id := uuid.NewString()

	_, err := sqlDB.ExecContext(ctx,
		`INSERT INTO _admins (id, email, password_hash) VALUES (?, ?, ?)`,
		id, email, passwordHash,
	)
	if err != nil {
		if isUniqueConstraintErr(err) {
			return nil, errEmailTaken
		}
		return nil, fmt.Errorf("insert admin: %w", err)
	}

	return getAdminByID(ctx, sqlDB, id)
}

func getAdminByEmail(ctx context.Context, sqlDB *sql.DB, email string) (*admin, error) {
	row := sqlDB.QueryRowContext(ctx,
		`SELECT id, email, password_hash, created, updated FROM _admins WHERE email = ?`,
		email,
	)
	return scanAdmin(row)
}

func getAdminByID(ctx context.Context, sqlDB *sql.DB, id string) (*admin, error) {
	row := sqlDB.QueryRowContext(ctx,
		`SELECT id, email, password_hash, created, updated FROM _admins WHERE id = ?`,
		id,
	)
	return scanAdmin(row)
}

func scanAdmin(row *sql.Row) (*admin, error) {
	var a admin
	if err := row.Scan(&a.ID, &a.Email, &a.PasswordHash, &a.Created, &a.Updated); err != nil {
		return nil, err
	}
	return &a, nil
}
