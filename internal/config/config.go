// Package config loads onebox server configuration from environment
// variables, with sane defaults for local development.
package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

	// AnthropicAPIKey backs the Anthropic branch of the LLM gateway (and
	// is what /api/rag/answer uses by default, via AnthropicModel).
	AnthropicAPIKey string
	AnthropicModel  string

	// OpenAIChatAPIKey/BaseURL back the OpenAI branch of the LLM gateway.
	// Kept separate from Embedding* since a self-hoster may use different
	// keys (or providers entirely) for chat vs. embeddings.
	OpenAIChatAPIKey  string
	OpenAIChatBaseURL string

	// OllamaBaseURL backs the Ollama branch of the LLM gateway, and is
	// reused for embeddings when EmbeddingProvider is "ollama" — it's the
	// same local daemon either way.
	OllamaBaseURL string

	// RateLimitPerMinute caps chat requests per user per minute.
	RateLimitPerMinute int
	// MonthlySpendCapUSD caps a user's estimated monthly LLM spend; 0
	// means unlimited.
	MonthlySpendCapUSD float64

	// CORSOrigins is who may call the API from a browser. onebox is a
	// backend *for* other frontends (a separate dev server, a static
	// site, a mobile webview) running on a different origin, so this
	// defaults wide open like other self-hosted BaaS tools (PocketBase,
	// Supabase's anon key) — lock it down with ONEBOX_CORS_ORIGINS for a
	// production deployment.
	CORSOrigins []string
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

		OpenAIChatAPIKey:  os.Getenv("ONEBOX_OPENAI_API_KEY"),
		OpenAIChatBaseURL: os.Getenv("ONEBOX_OPENAI_BASE_URL"),

		OllamaBaseURL: getEnv("ONEBOX_OLLAMA_BASE_URL", "http://localhost:11434"),

		RateLimitPerMinute: getEnvInt("ONEBOX_RATE_LIMIT_PER_MINUTE", 20),
		MonthlySpendCapUSD: getEnvFloat("ONEBOX_MONTHLY_SPEND_CAP_USD", 5.0),

		CORSOrigins: getEnvList("ONEBOX_CORS_ORIGINS", []string{"*"}),
	}
}

func getEnvList(key string, fallback []string) []string {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	if len(out) == 0 {
		return fallback
	}
	return out
}

func getEnvInt(key string, fallback int) int {
	if raw := os.Getenv(key); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			return n
		}
	}
	return fallback
}

func getEnvFloat(key string, fallback float64) float64 {
	if raw := os.Getenv(key); raw != "" {
		if f, err := strconv.ParseFloat(raw, 64); err == nil {
			return f
		}
	}
	return fallback
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
