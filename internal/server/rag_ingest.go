package server

import (
	"context"
	"database/sql"
	"fmt"
	"log"

	"onebox/internal/embeddings"
)

// ingestSource runs extract -> chunk -> embed -> store for one source in
// the background (kicked off by handleCreateRAGSource via `go`). Failures
// are recorded on the source row rather than returned, since this runs
// detached from any HTTP request by the time they'd happen.
func ingestSource(sqlDB *sql.DB, provider embeddings.Provider, ownerID string, src *ragSource, content []byte) {
	ctx := context.Background()

	if err := setRAGSourceStatus(ctx, sqlDB, src.ID, "processing", "", 0); err != nil {
		log.Printf("rag ingest %s: failed to mark processing: %v", src.ID, err)
		return
	}

	text, err := extractText(src.Filename, content)
	if err != nil {
		failIngest(ctx, sqlDB, src.ID, err)
		return
	}

	chunks := chunkText(text)
	if len(chunks) == 0 {
		failIngest(ctx, sqlDB, src.ID, fmt.Errorf("no extractable text found in %q", src.Filename))
		return
	}

	if provider == nil {
		failIngest(ctx, sqlDB, src.ID, fmt.Errorf("no embedding provider configured (set ONEBOX_EMBEDDING_API_KEY or run Ollama)"))
		return
	}

	vectors, err := provider.Embed(ctx, chunks)
	if err != nil {
		failIngest(ctx, sqlDB, src.ID, fmt.Errorf("embed chunks: %w", err))
		return
	}

	if err := insertRAGChunks(ctx, sqlDB, src.ID, ownerID, chunks, vectors); err != nil {
		failIngest(ctx, sqlDB, src.ID, err)
		return
	}

	if err := setRAGSourceStatus(ctx, sqlDB, src.ID, "done", "", len(chunks)); err != nil {
		log.Printf("rag ingest %s: failed to mark done: %v", src.ID, err)
	}
}

func failIngest(ctx context.Context, sqlDB *sql.DB, id string, cause error) {
	log.Printf("rag ingest %s failed: %v", id, cause)
	if err := setRAGSourceStatus(ctx, sqlDB, id, "error", cause.Error(), 0); err != nil {
		log.Printf("rag ingest %s: failed to record error status: %v", id, err)
	}
}
