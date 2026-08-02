// Package server wires the chi router, middleware, and API routes for the
// onebox HTTP server.
package server

import (
	"context"
	"database/sql"
	"log"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"

	"onebox/internal/config"
	"onebox/internal/embeddings"
	"onebox/internal/llm"
	"onebox/internal/webui"
)

// providerBundle groups the embedding/LLM clients built from the
// current effective config (env vars overlaid with any encrypted
// _settings overrides). Held behind an atomic pointer so saving a
// provider key via the dashboard (see settings_handlers.go) takes effect
// on the next request immediately, without a restart, while in-flight
// requests keep using whichever bundle they already loaded.
type providerBundle struct {
	embedding embeddings.Provider
	llm       *llm.Router
	// chat is the resolved {provider, model} onebox itself uses when it
	// has to pick a chat backend on the operator's behalf — the admin
	// chatbot panel and /api/rag/answer. See resolveChatSelection.
	chat chatSelection
}

// Server holds shared dependencies for HTTP handlers.
type Server struct {
	cfg         config.Config
	db          *sql.DB
	hub         *realtimeHub
	chatCache   *chatCache
	rateLimiter *rateLimiter
	providers   atomic.Pointer[providerBundle]
	// startedAt backs the AI heartbeat/metrics dashboard's uptime figure —
	// see internal/server/metrics.go.
	startedAt time.Time
	// activeStreams counts SSE chat/LLM streams currently in flight
	// (incremented at the top of streamChatReply/streamLLMChat,
	// decremented via defer) — the only way to answer "how many streaming
	// requests are active right now," since _logs/_usage only ever record
	// a request after it has already finished. See metrics.go.
	activeStreams atomic.Int64
}

// New builds a Server and its router. Provider sub-clients are left nil
// when unconfigured (no API key), so a self-hoster who hasn't set one up
// gets a clear per-request error instead of a broken client.
func New(cfg config.Config, sqlDB *sql.DB) *Server {
	s := &Server{cfg: cfg, db: sqlDB, hub: newRealtimeHub(), chatCache: newChatCache(), rateLimiter: newRateLimiter(cfg.RateLimitPerMinute), startedAt: time.Now()}
	if err := s.reloadProviders(context.Background()); err != nil {
		log.Printf("load provider settings: %v (falling back to env-only config)", err)
		s.providers.Store(&providerBundle{
			embedding: buildEmbeddingProvider(cfg),
			llm:       buildLLMRouter(cfg),
			chat:      resolveChatSelection(cfg, nil),
		})
	}
	return s
}

// reloadProviders rebuilds the embedding/LLM providers from cfg overlaid
// with any encrypted overrides in _settings, then atomically swaps them
// in. Called at startup and after every successful PUT /api/settings.
func (s *Server) reloadProviders(ctx context.Context) error {
	stored, err := getAllSettings(ctx, s.db, s.cfg.JWTSecret)
	if err != nil {
		return err
	}

	effective := s.cfg
	if v, ok := stored[settingAnthropicAPIKey]; ok {
		effective.AnthropicAPIKey = v
	}
	if v, ok := stored[settingAnthropicModel]; ok {
		effective.AnthropicModel = v
	}
	if v, ok := stored[settingOpenAIAPIKey]; ok {
		effective.OpenAIChatAPIKey = v
	}
	if v, ok := stored[settingOpenAIBaseURL]; ok {
		effective.OpenAIChatBaseURL = v
	}
	if v, ok := stored[settingEmbeddingProvider]; ok {
		effective.EmbeddingProvider = v
	}
	if v, ok := stored[settingEmbeddingAPIKey]; ok {
		effective.EmbeddingAPIKey = v
	}
	if v, ok := stored[settingEmbeddingBaseURL]; ok {
		effective.EmbeddingBaseURL = v
	}
	if v, ok := stored[settingEmbeddingModel]; ok {
		effective.EmbeddingModel = v
	}
	if v, ok := stored[settingOllamaBaseURL]; ok {
		effective.OllamaBaseURL = v
	}
	if v, ok := stored[settingChatProvider]; ok {
		effective.ChatProvider = v
	}
	if v, ok := stored[settingChatModel]; ok {
		effective.ChatModel = v
	}

	s.providers.Store(&providerBundle{
		embedding: buildEmbeddingProvider(effective),
		llm:       buildLLMRouter(effective),
		chat:      resolveChatSelection(effective, stored),
	})
	return nil
}

func buildEmbeddingProvider(cfg config.Config) embeddings.Provider {
	switch cfg.EmbeddingProvider {
	case "ollama":
		return embeddings.NewOllamaProvider(cfg.OllamaBaseURL, cfg.EmbeddingModel)
	case "voyage":
		// Voyage AI's embeddings endpoint matches OpenAI's request/response
		// shape (model, input[] -> data[].embedding/.index), so it's the
		// same client with a different default base URL.
		if cfg.EmbeddingAPIKey == "" {
			return nil
		}
		baseURL := cfg.EmbeddingBaseURL
		if baseURL == "" {
			baseURL = "https://api.voyageai.com/v1"
		}
		return embeddings.NewOpenAIProvider(baseURL, cfg.EmbeddingAPIKey, cfg.EmbeddingModel)
	default:
		if cfg.EmbeddingAPIKey == "" {
			return nil
		}
		return embeddings.NewOpenAIProvider(cfg.EmbeddingBaseURL, cfg.EmbeddingAPIKey, cfg.EmbeddingModel)
	}
}

func buildLLMRouter(cfg config.Config) *llm.Router {
	router := &llm.Router{Ollama: llm.NewOllamaClient(cfg.OllamaBaseURL)}
	if cfg.AnthropicAPIKey != "" {
		router.Anthropic = llm.NewAnthropicClient("", cfg.AnthropicAPIKey)
	}
	if cfg.OpenAIChatAPIKey != "" {
		router.OpenAI = llm.NewOpenAIClient(cfg.OpenAIChatBaseURL, cfg.OpenAIChatAPIKey)
	}
	return router
}

// Router builds the chi router with middleware and all routes mounted.
func (s *Server) Router() http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	// 120s (not the previous 30s): chat/RAG requests call out to an LLM
	// provider — often a local Ollama instance running on CPU — and a
	// cold model load or a long generation can legitimately take well
	// over 30s without anything actually being wrong. The chatbot
	// frontend never surfaces this value to the admin either way (see
	// app.js's chat retry/error-hiding logic); it just gives slow-but-
	// healthy requests enough room to finish instead of being cut off.
	r.Use(middleware.Timeout(120 * time.Second))
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   s.cfg.CORSOrigins,
		AllowedMethods:   []string{"GET", "POST", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Authorization", "Content-Type"},
		AllowCredentials: false,
		MaxAge:           300,
	}))

	r.Mount("/_/", http.StripPrefix("/_/", webui.Handler()))
	r.Get("/chat/{token}", handlePublicChatPage)

	r.Route("/api", func(r chi.Router) {
		r.Use(s.requestLogger)
		r.Get("/health", s.handleHealth)
		r.Get("/setup-status", s.handleSetupStatus)
		r.Post("/login", s.handleUnifiedLogin)
		r.With(s.requireAdminAuth).Get("/logs", s.handleListLogs)
		r.With(s.requireAdminAuth).Get("/logs/metrics", s.handleLogsMetrics)
		r.With(s.requireAdminAuth).Get("/heartbeat", s.handleHeartbeat)
		r.With(s.requireAdminAuth).Post("/chat", s.handleChatbot)
		r.Post("/chat/{token}", s.handlePublicChat)

		r.Route("/chat-share", func(r chi.Router) {
			r.Use(s.requireAdminAuth)
			r.Get("/", s.handleGetChatShare)
			r.Post("/enable", s.handleEnableChatShare)
			r.Post("/disable", s.handleDisableChatShare)
			r.Post("/regenerate", s.handleRegenerateChatShare)
		})

		// Chat attachments (multimodal chat): admin-only, same as POST
		// /api/chat itself — see chatbotRequest.AttachmentIDs' doc comment
		// for why this doesn't extend to the public share link.
		r.Route("/chat-attachments", func(r chi.Router) {
			r.Use(s.requireAdminAuth)
			r.Post("/", s.handleUploadChatAttachment)
			r.Delete("/{id}", s.handleDeleteChatAttachment)
		})

		r.Route("/auth", func(r chi.Router) {
			r.Post("/signup", s.handleSignup)
			r.Post("/login", s.handleLogin)
			r.Post("/reset-password", s.handleResetPassword)
			r.Post("/recover-password", s.handleRecoverPassword)

			r.Group(func(r chi.Router) {
				r.Use(s.requireUserAuth)
				r.Get("/me", s.handleMe)
				r.Patch("/me", s.handleUpdateMe)
				r.Post("/me/avatar", s.handleUploadAvatar)
				r.Delete("/me/avatar", s.handleRemoveAvatar)
				r.Post("/change-password", s.handleChangePassword)
			})

			r.With(s.requireAnyAuth).Post("/regenerate-recovery-phrase", s.handleRegenerateRecoveryPhrase)
		})

		r.Route("/admins", func(r chi.Router) {
			r.Post("/signup", s.handleAdminSignup)
			r.Post("/login", s.handleAdminLogin)
			r.With(s.requireAdminAuth).Post("/password-resets", s.handleCreatePasswordReset)

			r.Group(func(r chi.Router) {
				r.Use(s.requireAdminAuth)
				r.Get("/", s.handleListAdmins)
				r.Post("/promote", s.handlePromoteToAdmin)
				r.Post("/demote", s.handleDemoteAdmin)
				r.Get("/me", s.handleAdminMe)
				r.Patch("/me", s.handleUpdateAdminMe)
				r.Post("/me/avatar", s.handleUploadAdminAvatar)
				r.Delete("/me/avatar", s.handleRemoveAdminAvatar)
			})
		})

		r.With(s.realtimeAuth).Get("/realtime", s.handleRealtime)

		r.Route("/files", func(r chi.Router) {
			r.With(s.requireAnyAuth).Post("/", s.handleUploadFile)
			r.With(s.requireAnyAuth).Get("/", s.handleListFiles)
			r.With(s.optionalAuth).Get("/{id}", s.handleServeFile)
			r.With(s.optionalAuth).Delete("/{id}", s.handleDeleteFile)
		})

		r.Route("/rag", func(r chi.Router) {
			r.Route("/sources", func(r chi.Router) {
				r.With(s.requireAnyAuth).Post("/", s.handleCreateRAGSource)
				r.With(s.requireAnyAuth).Get("/", s.handleListRAGSources)
				r.With(s.optionalAuth).Get("/{id}", s.handleGetRAGSource)
				r.With(s.optionalAuth).Delete("/{id}", s.handleDeleteRAGSource)
			})
			r.With(s.requireAnyAuth).Post("/query", s.handleRAGQuery)
			r.With(s.requireAnyAuth).Post("/answer", s.handleRAGAnswer)
		})

		r.With(s.requireAnyAuth).Post("/llm/chat", s.handleLLMChat)
		r.With(s.requireAnyAuth).Get("/usage", s.handleUsage)

		r.Route("/backups", func(r chi.Router) {
			r.Use(s.requireAdminAuth)
			r.Get("/export", s.handleExportBackup)
			r.Post("/import", s.handleImportBackup)
		})

		r.Route("/settings", func(r chi.Router) {
			r.Use(s.requireAdminAuth)
			r.Get("/", s.handleGetSettings)
			r.Put("/", s.handleUpdateSettings)
			r.Post("/test-connection", s.handleTestConnection)
			r.Get("/ollama-models", s.handleOllamaModels)
		})

		r.Route("/collections", func(r chi.Router) {
			r.Group(func(r chi.Router) {
				r.Use(s.requireAdminAuth)
				r.Post("/", s.handleCreateCollection)
				r.Get("/", s.handleListCollections)
				r.Get("/{name}", s.handleGetCollection)
				r.Patch("/{name}", s.handleUpdateCollectionSchema)
				r.Delete("/{name}", s.handleDeleteCollection)
				r.Get("/{name}/export", s.handleExportCollection)
				r.Post("/{name}/import/preview", s.handleImportPreview)
				r.Post("/{name}/import", s.handleImportCollection)
			})

			r.Route("/{name}/records", func(r chi.Router) {
				r.Use(s.optionalAuth)
				r.Use(s.loadCollection)
				r.Get("/", s.handleListRecords)
				r.Post("/", s.handleCreateRecord)
				r.Route("/{id}", func(r chi.Router) {
					r.Get("/", s.handleGetRecord)
					r.Patch("/", s.handleUpdateRecord)
					r.Delete("/", s.handleDeleteRecord)
				})
			})
		})
	})

	return r
}
