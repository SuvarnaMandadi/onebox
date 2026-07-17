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
)

// Server holds shared dependencies for HTTP handlers.
type Server struct {
	cfg config.Config
	db  *sql.DB
}

// New builds a Server and its router.
func New(cfg config.Config, sqlDB *sql.DB) *Server {
	return &Server{cfg: cfg, db: sqlDB}
}

// Router builds the chi router with middleware and all routes mounted.
func (s *Server) Router() http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))

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
	})

	return r
}
