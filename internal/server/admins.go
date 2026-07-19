package server

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
)

type admin struct {
	ID                 string `json:"id"`
	Email              string `json:"email"`
	PasswordHash       string `json:"-"`
	FirstName          string `json:"first_name"`
	LastName           string `json:"last_name"`
	DisplayName        string `json:"display_name"`
	Phone              string `json:"phone"`
	AvatarFileID       string `json:"avatar_file_id,omitempty"`
	RecoveryPhraseHash string `json:"-"`
	Created            string `json:"created"`
	Updated            string `json:"updated"`
}

const adminColumns = "id, email, password_hash, first_name, last_name, display_name, phone, avatar_file_id, recovery_phrase_hash, created, updated"

func countAdmins(ctx context.Context, sqlDB *sql.DB) (int, error) {
	var count int
	err := sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM _admins`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count admins: %w", err)
	}
	return count, nil
}

func createAdmin(ctx context.Context, sqlDB *sql.DB, email, passwordHash, firstName, lastName, recoveryPhraseHash string) (*admin, error) {
	id := uuid.NewString()

	_, err := sqlDB.ExecContext(ctx,
		`INSERT INTO _admins (id, email, password_hash, first_name, last_name, recovery_phrase_hash) VALUES (?, ?, ?, ?, ?, ?)`,
		id, email, passwordHash, firstName, lastName, recoveryPhraseHash,
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
		`SELECT `+adminColumns+` FROM _admins WHERE email = ?`,
		email,
	)
	return scanAdmin(row)
}

func getAdminByID(ctx context.Context, sqlDB *sql.DB, id string) (*admin, error) {
	row := sqlDB.QueryRowContext(ctx,
		`SELECT `+adminColumns+` FROM _admins WHERE id = ?`,
		id,
	)
	return scanAdmin(row)
}

func scanAdmin(row *sql.Row) (*admin, error) {
	var a admin
	var avatarFileID, recoveryPhraseHash sql.NullString
	if err := row.Scan(&a.ID, &a.Email, &a.PasswordHash, &a.FirstName, &a.LastName, &a.DisplayName, &a.Phone, &avatarFileID, &recoveryPhraseHash, &a.Created, &a.Updated); err != nil {
		return nil, err
	}
	a.AvatarFileID = avatarFileID.String
	a.RecoveryPhraseHash = recoveryPhraseHash.String
	return &a, nil
}

// listAdmins returns every admin account, oldest first, for the Settings
// page's admin-management panel.
func listAdmins(ctx context.Context, sqlDB *sql.DB) ([]*admin, error) {
	rows, err := sqlDB.QueryContext(ctx, `SELECT `+adminColumns+` FROM _admins ORDER BY created`)
	if err != nil {
		return nil, fmt.Errorf("query admins: %w", err)
	}
	defer rows.Close()

	var out []*admin
	for rows.Next() {
		var a admin
		var avatarFileID, recoveryPhraseHash sql.NullString
		if err := rows.Scan(&a.ID, &a.Email, &a.PasswordHash, &a.FirstName, &a.LastName, &a.DisplayName, &a.Phone, &avatarFileID, &recoveryPhraseHash, &a.Created, &a.Updated); err != nil {
			return nil, fmt.Errorf("scan admin row: %w", err)
		}
		a.AvatarFileID = avatarFileID.String
		a.RecoveryPhraseHash = recoveryPhraseHash.String
		out = append(out, &a)
	}
	return out, rows.Err()
}

func deleteAdmin(ctx context.Context, sqlDB *sql.DB, id string) error {
	res, err := sqlDB.ExecContext(ctx, `DELETE FROM _admins WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete admin: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func updateAdminPasswordHash(ctx context.Context, sqlDB *sql.DB, id, passwordHash string) error {
	_, err := sqlDB.ExecContext(ctx,
		`UPDATE _admins SET password_hash = ?, updated = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ?`,
		passwordHash, id,
	)
	if err != nil {
		return fmt.Errorf("update admin password: %w", err)
	}
	return nil
}

func updateAdminRecoveryPhraseHash(ctx context.Context, sqlDB *sql.DB, id, recoveryPhraseHash string) error {
	_, err := sqlDB.ExecContext(ctx,
		`UPDATE _admins SET recovery_phrase_hash = ?, updated = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ?`,
		recoveryPhraseHash, id,
	)
	if err != nil {
		return fmt.Errorf("update admin recovery phrase: %w", err)
	}
	return nil
}

func updateAdminAvatar(ctx context.Context, sqlDB *sql.DB, id, avatarFileID string) (*admin, error) {
	_, err := sqlDB.ExecContext(ctx,
		`UPDATE _admins SET avatar_file_id = ?, updated = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ?`,
		avatarFileID, id,
	)
	if err != nil {
		return nil, fmt.Errorf("update admin avatar: %w", err)
	}
	return getAdminByID(ctx, sqlDB, id)
}

func updateAdminProfile(ctx context.Context, sqlDB *sql.DB, id, firstName, lastName, displayName, phone string) (*admin, error) {
	_, err := sqlDB.ExecContext(ctx,
		`UPDATE _admins SET first_name = ?, last_name = ?, display_name = ?, phone = ?, updated = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ?`,
		firstName, lastName, displayName, phone, id,
	)
	if err != nil {
		return nil, fmt.Errorf("update admin profile: %w", err)
	}
	return getAdminByID(ctx, sqlDB, id)
}
