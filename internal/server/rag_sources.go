package server

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
)

type ragSource struct {
	ID         string `json:"id"`
	OwnerID    string `json:"owner_id,omitempty"`
	FileID     string `json:"file_id"`
	Filename   string `json:"filename"`
	Status     string `json:"status"` // pending, processing, done, error
	ChunkCount int    `json:"chunk_count"`
	Error      string `json:"error,omitempty"`
	Created    string `json:"created"`
	Updated    string `json:"updated"`
}

func createRAGSource(ctx context.Context, sqlDB *sql.DB, ownerID, fileID, filename string) (*ragSource, error) {
	id := uuid.NewString()
	_, err := sqlDB.ExecContext(ctx,
		`INSERT INTO _rag_sources (id, owner_id, file_id, filename) VALUES (?, ?, ?, ?)`,
		id, nullableString(ownerID), fileID, filename,
	)
	if err != nil {
		return nil, fmt.Errorf("insert rag source: %w", err)
	}
	return getRAGSource(ctx, sqlDB, id)
}

func getRAGSource(ctx context.Context, sqlDB *sql.DB, id string) (*ragSource, error) {
	row := sqlDB.QueryRowContext(ctx,
		`SELECT id, owner_id, file_id, filename, status, chunk_count, error, created, updated FROM _rag_sources WHERE id = ?`, id,
	)
	return scanRAGSource(row)
}

func scanRAGSource(row *sql.Row) (*ragSource, error) {
	var s ragSource
	var owner, errMsg sql.NullString
	if err := row.Scan(&s.ID, &owner, &s.FileID, &s.Filename, &s.Status, &s.ChunkCount, &errMsg, &s.Created, &s.Updated); err != nil {
		return nil, err
	}
	s.OwnerID = owner.String
	s.Error = errMsg.String
	return &s, nil
}

func setRAGSourceStatus(ctx context.Context, sqlDB *sql.DB, id, status, errMsg string, chunkCount int) error {
	_, err := sqlDB.ExecContext(ctx,
		`UPDATE _rag_sources SET status = ?, error = ?, chunk_count = ?, updated = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ?`,
		status, nullableString(errMsg), chunkCount, id,
	)
	if err != nil {
		return fmt.Errorf("update rag source status: %w", err)
	}
	return nil
}

// deleteRAGSource removes the source row (cascading to its chunks via the
// FK) and its underlying file. Callers are responsible for checking
// ownership before calling this.
func deleteRAGSource(ctx context.Context, sqlDB *sql.DB, filesDir string, src *ragSource) error {
	if _, err := sqlDB.ExecContext(ctx, `DELETE FROM _rag_sources WHERE id = ?`, src.ID); err != nil {
		return fmt.Errorf("delete rag source: %w", err)
	}
	if err := deleteFileRecord(ctx, sqlDB, src.FileID); err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("delete underlying file record: %w", err)
	}
	removeStoredFile(filesDir, src.FileID)
	return nil
}

type ragChunkRow struct {
	ID        string
	SourceID  string
	OwnerID   string
	Position  int
	Text      string
	Embedding []float32
}

func insertRAGChunks(ctx context.Context, sqlDB *sql.DB, sourceID, ownerID string, texts []string, vectors [][]float32) error {
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO _rag_chunks (id, source_id, owner_id, position, text, embedding) VALUES (?, ?, ?, ?, ?, ?)`,
	)
	if err != nil {
		return fmt.Errorf("prepare insert: %w", err)
	}
	defer stmt.Close()

	for i, text := range texts {
		_, err := stmt.ExecContext(ctx, uuid.NewString(), sourceID, nullableString(ownerID), i, text, encodeVector(vectors[i]))
		if err != nil {
			return fmt.Errorf("insert chunk %d: %w", i, err)
		}
	}
	return tx.Commit()
}

// ragChunksForOwner returns every chunk visible to a query: the caller's
// own chunks, or every chunk for an admin. This keeps RAG search scoped
// per-user by default, matching the rest of the app's ownership model.
func ragChunksForOwner(ctx context.Context, sqlDB *sql.DB, ownerID string, isAdmin bool) ([]ragChunkRow, error) {
	var rows *sql.Rows
	var err error
	if isAdmin {
		rows, err = sqlDB.QueryContext(ctx, `SELECT id, source_id, owner_id, position, text, embedding FROM _rag_chunks`)
	} else {
		rows, err = sqlDB.QueryContext(ctx, `SELECT id, source_id, owner_id, position, text, embedding FROM _rag_chunks WHERE owner_id = ?`, ownerID)
	}
	if err != nil {
		return nil, fmt.Errorf("query chunks: %w", err)
	}
	defer rows.Close()

	var out []ragChunkRow
	for rows.Next() {
		var c ragChunkRow
		var owner sql.NullString
		var embedding []byte
		if err := rows.Scan(&c.ID, &c.SourceID, &owner, &c.Position, &c.Text, &embedding); err != nil {
			return nil, fmt.Errorf("scan chunk: %w", err)
		}
		c.OwnerID = owner.String
		c.Embedding = decodeVector(embedding)
		out = append(out, c)
	}
	return out, rows.Err()
}
