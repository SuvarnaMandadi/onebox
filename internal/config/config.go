// Package config loads onebox server configuration from environment
// variables, with sane defaults for local development.
package config

import (
	"os"
	"path/filepath"
	"strconv"
)

const defaultMaxUploadSize = 20 * 1024 * 1024 // 20 MiB

type Config struct {
	// Addr is the host:port the HTTP server listens on.
	Addr string
	// DataDir holds the SQLite database, uploaded files, and other
	// persistent state.
	DataDir string
	// DBPath is the path to the main SQLite database file.
	DBPath string
	// FilesDir holds uploaded file contents.
	FilesDir string
	// JWTSecret signs auth session tokens.
	JWTSecret string
	// MaxUploadSize is the largest file, in bytes, /api/files will accept.
	MaxUploadSize int64

	// EmbeddingProvider selects the RAG engine's embedding backend:
	// "openai" (or any OpenAI-compatible endpoint) or "ollama".
	EmbeddingProvider string
	EmbeddingBaseURL  string
	EmbeddingAPIKey   string
	EmbeddingModel    string

	// AnthropicAPIKey/Model back /api/rag/answer's LLM call. The full
	// multi-provider gateway (Month 4) will supersede this.
	AnthropicAPIKey string
	AnthropicModel  string
}

// Load builds a Config from environment variables, falling back to
// development defaults for anything unset.
func Load() Config {
	dataDir := getEnv("ONEBOX_DATA_DIR", "./onebox_data")
	secret := getEnv("ONEBOX_JWT_SECRET", "dev-insecure-secret-change-me")

	maxUpload := int64(defaultMaxUploadSize)
	if raw := os.Getenv("ONEBOX_MAX_UPLOAD_SIZE"); raw != "" {
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil && n > 0 {
			maxUpload = n
		}
	}

	return Config{
		Addr:          getEnv("ONEBOX_ADDR", ":8090"),
		DataDir:       dataDir,
		DBPath:        filepath.Join(dataDir, "data.db"),
		FilesDir:      filepath.Join(dataDir, "files"),
		JWTSecret:     secret,
		MaxUploadSize: maxUpload,

		EmbeddingProvider: getEnv("ONEBOX_EMBEDDING_PROVIDER", "openai"),
		EmbeddingBaseURL:  os.Getenv("ONEBOX_EMBEDDING_BASE_URL"),
		EmbeddingAPIKey:   os.Getenv("ONEBOX_EMBEDDING_API_KEY"),
		EmbeddingModel:    getEnv("ONEBOX_EMBEDDING_MODEL", "text-embedding-3-small"),

		AnthropicAPIKey: os.Getenv("ONEBOX_ANTHROPIC_API_KEY"),
		AnthropicModel:  getEnv("ONEBOX_ANTHROPIC_MODEL", "claude-sonnet-5"),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
