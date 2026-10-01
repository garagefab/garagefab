package server

import (
	"context"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/garagefab/garagefab/internal/config"
	"github.com/garagefab/garagefab/internal/store"
)

// JobEngine defines pipeline actions required by the HTTP server.
type JobEngine interface {
	Approve(ctx context.Context, jobID int64, headSHA string) error
	Reject(ctx context.Context, jobID int64, note string) error
	Cancel(ctx context.Context, jobID int64) error
	Retry(ctx context.Context, jobID int64) error
}

// JobScheduler defines scheduling actions required by the HTTP server.
type JobScheduler interface {
	Wake()
}

// Server coordinates the HTTP router, middleware, APIs, and static UI file serving.
type Server struct {
	Router     *chi.Mux
	Config     *config.Config
	DB         *store.DB
	Engine     JobEngine
	Scheduler  JobScheduler
	UIFS       fs.FS
	httpServer *http.Server
}

// NewServer initializes a new Server instance with Chi router and registered routes.
func NewServer(cfg *config.Config, db *store.DB, engine JobEngine, scheduler JobScheduler, uiFS fs.FS) *Server {
	s := &Server{
		Config:    cfg,
		DB:        db,
		Engine:    engine,
		Scheduler: scheduler,
		UIFS:      uiFS,
	}

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(s.hostHeaderMiddleware)

	// API routes
	r.Route("/api", func(api chi.Router) {
		api.Get("/health", s.handleHealth)
		api.Post("/session", s.handleCreateSession)

		// Authenticated routes (SEC-1, SEC-3)
		api.Group(func(protected chi.Router) {
			protected.Use(s.authMiddleware)

			// Projects (PRJ-1..5)
			protected.Get("/projects", s.handleListProjects)
			protected.Post("/projects", s.handleCreateProject)
			protected.Get("/projects/{id}", s.handleGetProject)

			// Jobs (INT-1, PIP-6, PIP-7)
			protected.Get("/jobs", s.handleListJobs)
			protected.Post("/jobs", s.handleCreateJob)
			protected.Get("/jobs/{id}", s.handleGetJob)
			protected.Post("/jobs/{id}/cancel", s.handleCancelJob)
			protected.Post("/jobs/{id}/retry", s.handleRetryJob)

			// SSE Events Stream (LOG-4)
			protected.Get("/events", s.handleEventsSSE)

			// Interactive session-only endpoints (SEC-4, APR-7)
			protected.Group(func(sessionOnly chi.Router) {
				sessionOnly.Use(s.requireSessionOnlyMiddleware)
				sessionOnly.Post("/jobs/{id}/approve", s.handleApproveJob)
				sessionOnly.Post("/jobs/{id}/reject", s.handleRejectJob)
			})
		})
	})

	// Static UI / SPA fallback
	if uiFS != nil {
		s.setupStaticFiles(r)
	}

	s.Router = r
	return s
}

// hostHeaderMiddleware validates that incoming requests use a loopback Host header (SEC-2).
func (s *Server) hostHeaderMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}

		if host != "127.0.0.1" && host != "localhost" {
			http.Error(w, "Forbidden: invalid host header", http.StatusForbidden)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// setupStaticFiles serves UI assets and routes unhandled non-API paths to index.html.
func (s *Server) setupStaticFiles(r *chi.Mux) {
	fileServer := http.FileServer(http.FS(s.UIFS))

	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		// API endpoints that don't match should return 404 JSON, not SPA html
		if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/api" {
			http.NotFound(w, r)
			return
		}

		// Try opening the requested file in uiFS
		cleaned := strings.TrimPrefix(r.URL.Path, "/")
		if cleaned == "" {
			cleaned = "index.html"
		}

		f, err := s.UIFS.Open(cleaned)
		if err == nil {
			_ = f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}

		// If file does not exist, serve index.html for SPA client-side routing
		indexContent, err := fs.ReadFile(s.UIFS, "index.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(indexContent)
	})
}

// Start begins listening and serving HTTP requests.
func (s *Server) Start(addr string) error {
	s.httpServer = &http.Server{
		Addr:    addr,
		Handler: s.Router,
	}

	if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("server: listen: %w", err)
	}
	return nil
}

// Shutdown gracefully stops the HTTP server.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.httpServer != nil {
		return s.httpServer.Shutdown(ctx)
	}
	return nil
}
