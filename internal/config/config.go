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

// defaultJWTSecret is the fallback used when ONEBOX_JWT_SECRET is unset —
// deliberately public (it's right here in the source, which anyone
// deploying onebox can read) and deliberately never blocking startup: a
// fresh local dev/CI checkout must keep working with zero configuration.
// See Load's doc comment for why this being unchanged is loud, not silent.
const defaultJWTSecret = "dev-insecure-secret-change-me"

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
	// JWTSecret signs auth session tokens, and (see settings_crypto.go's
	// settingsKey) also derives the AES-256 key that encrypts provider API
	// keys at rest — one secret backs both.
	JWTSecret string
	// JWTSecretIsDefault reports whether JWTSecret is still defaultJWTSecret
	// because ONEBOX_JWT_SECRET was never set — cmd/onebox/main.go logs a
	// loud startup warning off this rather than the caller re-deriving the
	// same comparison (or, worse, hardcoding defaultJWTSecret's literal
	// value a second time).
	JWTSecretIsDefault bool
	// MaxUploadSize is the largest file, in bytes, /api/files will accept.
	MaxUploadSize int64

	// EmbeddingProvider selects the RAG engine's embedding backend:
	// "openai" (or any OpenAI-compatible endpoint) or "ollama".
	EmbeddingProvider string
	EmbeddingBaseURL  string
	EmbeddingAPIKey   string
	EmbeddingModel    string

	// AnthropicAPIKey backs the Anthropic branch of the LLM gateway. Used
	// whenever a caller (directly, or the dashboard's Chat Provider) picks
	// an Anthropic/Claude model.
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

	// ChatProvider and ChatModel select which backend the dashboard's own
	// chat surfaces — the admin chatbot panel and /api/rag/answer — use.
	// This is completely independent of EmbeddingProvider (RAG ingestion
	// keeps working on whatever embedding backend is configured no matter
	// what ChatProvider is set to) and independent of the per-request
	// model routing POST /api/llm/chat still does for direct API callers
	// (see llm.ProviderKind) — those are unchanged.
	//
	// One of "ollama" (default — no key required, works out of the box
	// for a local install), "anthropic", or "openai". Overridable at
	// runtime via the chat_provider/chat_model settings, which take
	// precedence over these env-var defaults; see reloadProviders.
	ChatProvider string
	ChatModel    string

	// RateLimitPerMinute caps chat requests per user per minute.
	RateLimitPerMinute int
	// AuthRateLimitPerMinute caps unauthenticated auth-endpoint requests
	// (login/signup/password-reset, both _users and _admins) per source IP
	// per minute — a brute-force/credential-stuffing guard. Unlike
	// RateLimitPerMinute (keyed per authenticated user), this has to be
	// keyed by IP since there's no user identity yet on these routes.
	AuthRateLimitPerMinute int
	// MonthlySpendCapUSD caps a user's estimated monthly LLM spend; 0
	// means unlimited.
	MonthlySpendCapUSD float64

	// BackupIntervalHours, when > 0, starts a background scheduler (see
	// startBackupScheduler in backups.go) that creates a full backup on
	// this interval — 0 (the default) means scheduled backups are off,
	// since silently writing snapshots to disk on a fresh install with no
	// retention awareness would be a surprising default, not a helpful
	// one. Manual backups (POST /api/backups) work regardless of this
	// setting.
	BackupIntervalHours int
	// BackupRetentionCount bounds how many backups (manual + scheduled
	// combined) are kept on disk — the oldest are deleted past this count,
	// checked after every backup a scheduled run creates. A manual backup
	// never gets auto-deleted by retention on its own action, only as the
	// oldest row once the count is exceeded by a later backup, same as any
	// other backup.
	BackupRetentionCount int

	// CORSOrigins is who may call the API from a browser. onebox is a
	// backend *for* other frontends (a separate dev server, a static
	// site, a mobile webview) running on a different origin, so this
	// defaults wide open like other self-hosted BaaS tools (PocketBase,
	// Supabase's anon key) — lock it down with ONEBOX_CORS_ORIGINS for a
	// production deployment.
	CORSOrigins []string

	// Version is the build version string (set via -ldflags by
	// scripts/build-release.sh), surfaced by GET /api/health for the
	// dashboard footer. Defaults to "dev" for local builds.
	Version string
}

// Load builds a Config from environment variables, falling back to
// development defaults for anything unset.
func Load() Config {
	dataDir := getEnv("ONEBOX_DATA_DIR", "./onebox_data")
	secret := getEnv("ONEBOX_JWT_SECRET", defaultJWTSecret)

	maxUpload := int64(defaultMaxUploadSize)
	if raw := os.Getenv("ONEBOX_MAX_UPLOAD_SIZE"); raw != "" {
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil && n > 0 {
			maxUpload = n
		}
	}

	return Config{
		Addr:               getEnv("ONEBOX_ADDR", ":8090"),
		DataDir:            dataDir,
		DBPath:             filepath.Join(dataDir, "data.db"),
		FilesDir:           filepath.Join(dataDir, "files"),
		JWTSecret:          secret,
		JWTSecretIsDefault: secret == defaultJWTSecret,
		MaxUploadSize:      maxUpload,

		EmbeddingProvider: getEnv("ONEBOX_EMBEDDING_PROVIDER", "openai"),
		EmbeddingBaseURL:  os.Getenv("ONEBOX_EMBEDDING_BASE_URL"),
		EmbeddingAPIKey:   os.Getenv("ONEBOX_EMBEDDING_API_KEY"),
		EmbeddingModel:    getEnv("ONEBOX_EMBEDDING_MODEL", "text-embedding-3-small"),

		AnthropicAPIKey: os.Getenv("ONEBOX_ANTHROPIC_API_KEY"),
		AnthropicModel:  getEnv("ONEBOX_ANTHROPIC_MODEL", "claude-sonnet-5"),

		OpenAIChatAPIKey:  os.Getenv("ONEBOX_OPENAI_API_KEY"),
		OpenAIChatBaseURL: os.Getenv("ONEBOX_OPENAI_BASE_URL"),

		OllamaBaseURL: getEnv("ONEBOX_OLLAMA_BASE_URL", "http://localhost:11434"),

		ChatProvider: getEnv("ONEBOX_CHAT_PROVIDER", "ollama"),
		ChatModel:    os.Getenv("ONEBOX_CHAT_MODEL"),

		RateLimitPerMinute:     getEnvInt("ONEBOX_RATE_LIMIT_PER_MINUTE", 20),
		AuthRateLimitPerMinute: getEnvInt("ONEBOX_AUTH_RATE_LIMIT_PER_MINUTE", 10),
		MonthlySpendCapUSD:     getEnvFloat("ONEBOX_MONTHLY_SPEND_CAP_USD", 5.0),

		BackupIntervalHours:  getEnvInt("ONEBOX_BACKUP_INTERVAL_HOURS", 0),
		BackupRetentionCount: getEnvInt("ONEBOX_BACKUP_RETENTION_COUNT", 10),

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
