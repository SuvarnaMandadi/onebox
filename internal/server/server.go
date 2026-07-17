// Package server wires the chi router, middleware, and API routes for the
// onebox HTTP server.
package server

import (
	"database/sql"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"onebox/internal/config"
	"onebox/internal/webui"
)

// Server holds shared dependencies for HTTP handlers.
type Server struct {
	cfg config.Config
	db  *sql.DB
	hub *realtimeHub
}

// New builds a Server and its router.
func New(cfg config.Config, sqlDB *sql.DB) *Server {
	return &Server{cfg: cfg, db: sqlDB, hub: newRealtimeHub()}
}

// Router builds the chi router with middleware and all routes mounted.
func (s *Server) Router() http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))

	r.Mount("/_/", http.StripPrefix("/_/", webui.Handler()))

	r.Route("/api", func(r chi.Router) {
		r.Get("/health", s.handleHealth)

		r.Route("/auth", func(r chi.Router) {
			r.Post("/signup", s.handleSignup)
			r.Post("/login", s.handleLogin)
		})

		r.Route("/admins", func(r chi.Router) {
			r.Post("/signup", s.handleAdminSignup)
			r.Post("/login", s.handleAdminLogin)
		})

		r.With(s.realtimeAuth).Get("/realtime", s.handleRealtime)

		r.Route("/files", func(r chi.Router) {
			r.With(s.requireAnyAuth).Post("/", s.handleUploadFile)
			r.With(s.requireAdminAuth).Get("/", s.handleListFiles)
			r.With(s.optionalAuth).Get("/{id}", s.handleServeFile)
			r.With(s.optionalAuth).Delete("/{id}", s.handleDeleteFile)
		})

		r.Route("/collections", func(r chi.Router) {
			r.Group(func(r chi.Router) {
				r.Use(s.requireAdminAuth)
				r.Post("/", s.handleCreateCollection)
				r.Get("/", s.handleListCollections)
				r.Get("/{name}", s.handleGetCollection)
				r.Delete("/{name}", s.handleDeleteCollection)
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
