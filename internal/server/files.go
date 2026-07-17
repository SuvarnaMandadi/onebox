package server

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"github.com/google/uuid"
)

type fileRecord struct {
	ID       string `json:"id"`
	OwnerID  string `json:"owner_id,omitempty"`
	Filename string `json:"filename"`
	Size     int64  `json:"size"`
	Mime     string `json:"mime"`
	Created  string `json:"created"`
}

// storeFileContent writes content to <filesDir>/<id> and returns the id.
// Files are stored under a UUID rather than the original filename so
// collisions and path traversal from user-supplied filenames aren't a
// concern; the original filename is preserved only in the DB row.
func storeFileContent(filesDir string, content []byte) (id string, err error) {
	if err := os.MkdirAll(filesDir, 0o755); err != nil {
		return "", fmt.Errorf("create files dir: %w", err)
	}
	id = uuid.NewString()
	path := filepath.Join(filesDir, id)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}
	return id, nil
}

// removeStoredFile deletes a stored file's content from disk, ignoring a
// missing file (already gone is not an error for a delete path).
func removeStoredFile(filesDir, id string) {
	_ = os.Remove(filepath.Join(filesDir, id))
}

func createFileRecord(ctx context.Context, sqlDB *sql.DB, id, ownerID, filename, mime string, size int64) (*fileRecord, error) {
	_, err := sqlDB.ExecContext(ctx,
		`INSERT INTO _files (id, owner_id, filename, path, size, mime) VALUES (?, ?, ?, ?, ?, ?)`,
		id, nullableString(ownerID), filename, id, size, mime,
	)
	if err != nil {
		return nil, fmt.Errorf("insert file record: %w", err)
	}
	return getFileByID(ctx, sqlDB, id)
}

func getFileByID(ctx context.Context, sqlDB *sql.DB, id string) (*fileRecord, error) {
	row := sqlDB.QueryRowContext(ctx,
		`SELECT id, owner_id, filename, size, mime, created FROM _files WHERE id = ?`, id,
	)
	var f fileRecord
	var owner sql.NullString
	if err := row.Scan(&f.ID, &owner, &f.Filename, &f.Size, &f.Mime, &f.Created); err != nil {
		return nil, err
	}
	f.OwnerID = owner.String
	return &f, nil
}

// listFiles returns up to limit+1 files (the extra row signals whether a
// next page exists), newest first, for the admin dashboard's file browser.
func listFiles(ctx context.Context, sqlDB *sql.DB, limit int, cursorCreated, cursorID string) ([]*fileRecord, error) {
	stmt := `SELECT id, owner_id, filename, size, mime, created FROM _files`
	args := []any{}
	if cursorCreated != "" {
		stmt += ` WHERE (created, id) < (?, ?)`
		args = append(args, cursorCreated, cursorID)
	}
	stmt += ` ORDER BY created DESC, id DESC LIMIT ?`
	args = append(args, limit+1)

	rows, err := sqlDB.QueryContext(ctx, stmt, args...)
	if err != nil {
		return nil, fmt.Errorf("list files: %w", err)
	}
	defer rows.Close()

	var out []*fileRecord
	for rows.Next() {
		var f fileRecord
		var owner sql.NullString
		if err := rows.Scan(&f.ID, &owner, &f.Filename, &f.Size, &f.Mime, &f.Created); err != nil {
			return nil, fmt.Errorf("scan file row: %w", err)
		}
		f.OwnerID = owner.String
		out = append(out, &f)
	}
	return out, rows.Err()
}

func deleteFileRecord(ctx context.Context, sqlDB *sql.DB, id string) error {
	res, err := sqlDB.ExecContext(ctx, `DELETE FROM _files WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete file record: %w", err)
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

// fileOwnerMatches reports whether the requester (from context) is allowed
// to read/delete a file: the owner, or an admin.
func fileOwnerMatches(ctx context.Context, ownerID string) bool {
	if _, ok := authAdminID(ctx); ok {
		return true
	}
	uid, ok := authUserID(ctx)
	return ok && ownerID != "" && ownerID == uid
}
