package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"

	"onebox/internal/embeddings"
)

// providerBaseURL extracts the configured base URL from a concrete
// embeddings.Provider, for a friendlier "not reachable at <url>" error
// message. Returns "" for provider types with no meaningful URL.
func providerBaseURL(p embeddings.Provider) string {
	switch v := p.(type) {
	case *embeddings.OllamaProvider:
		return v.BaseURL
	case *embeddings.OpenAIProvider:
		return v.BaseURL
	default:
		return ""
	}
}

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
		failIngest(ctx, sqlDB, src.ID, fmt.Errorf("no embedding provider configured — set an embedding provider in Settings → Providers (Ollama or an API key)"))
		return
	}

	vectors, err := provider.Embed(ctx, chunks)
	if err != nil {
		failIngest(ctx, sqlDB, src.ID, errors.New(humanizeProviderError(err, "Embedding provider", providerBaseURL(provider))))
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
