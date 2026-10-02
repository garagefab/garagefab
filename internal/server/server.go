// Package server implements the HTTP server, routing, middleware, REST APIs,
// Server-Sent Events (SSE) hub, and embedded React SPA file serving.
//
// ==============================================================================
// ARCHITECTURAL ROLE & PATTERNS:
// Primary / Inbound Adapter (Hexagonal Architecture).
//
// In Hexagonal Architecture:
//   - `internal/server` is a "Driving / Inbound Adapter".
//   - It receives incoming HTTP requests from browsers or CLI tools, validates inputs,
//     and translates them into method calls on the domain core (`JobEngine`, `JobScheduler`)
//     or queries against the read storage (`store.DB`).
//   - It contains NO business pipeline logic (which lives entirely in `internal/factory`).
//
// GO CONCEPTS & JAVA / SPRING MVC COMPARISONS:
//
//  1. HTTP Routing with Chi (`go-chi/chi/v5`):
//     In Spring Boot: You define `@RestController`, `@GetMapping`, and `@PostMapping`.
//     In Go: Chi is a lightweight, idiomatic HTTP router that builds on Go's standard
//     `net/http.Handler` interface without any reflection or bytecode proxies.
//     Routes are grouped logically with `r.Route` and `r.Group`.
//
//  2. Middleware Pipelines:
//     Go middleware functions wrap handlers: `func(http.Handler) http.Handler`.
//     Equivalent to Java Servlet `Filter` or Spring `HandlerInterceptor`.
//
//  3. SPA Fallback (Single Page Application):
//     If a user directly visits a frontend route like `/projects/1/jobs/2`, the server
//     returns `index.html` with status 200, allowing React Router to render the page
//     client-side. Unmatched API paths (`/api/...`) return JSON 404 instead.
//
// ==============================================================================
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
	"github.com/garagefab/garagefab/internal/factory"
	"github.com/garagefab/garagefab/internal/store"
)

// JobEngine defines pipeline actions required by the HTTP server.
//
// Go Concept: Consumer-defined interface.
// `server` declares only the methods it needs from the factory engine.
type JobEngine interface {
	Approve(ctx context.Context, jobID int64, headSHA string) error
	Reject(ctx context.Context, jobID int64, note string) error
	Cancel(ctx context.Context, jobID int64) error
	Retry(ctx context.Context, jobID int64) error
	SubmitClarification(ctx context.Context, jobID int64, answers []factory.ClarificationAnswer) error
	GetEvidence(ctx context.Context, jobID int64) (*factory.EvidenceSummary, error)
	GetArtifact(ctx context.Context, jobID int64, name string) ([]byte, error)
	GetDiff(ctx context.Context, jobID int64) (string, error)
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
	UIFS       fs.FS        // Virtual filesystem serving embedded React build files
	httpServer *http.Server // Underlying standard library HTTP server
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

	// Standard production middleware stack
	r.Use(middleware.RequestID) // Injects unique X-Request-Id header into context
	r.Use(middleware.RealIP)    // Parses X-Forwarded-For or X-Real-IP
	r.Use(middleware.Logger)    // Structured access logging
	r.Use(middleware.Recoverer) // Catches panics and converts them to HTTP 500 (like @ControllerAdvice)
	r.Use(s.hostHeaderMiddleware)

	// API routes mounted under /api
	r.Route("/api", func(api chi.Router) {
		// Public endpoints (no auth required)
		api.Get("/health", s.handleHealth)
		api.Post("/session", s.handleCreateSession)

		// Protected endpoints (require Bearer Token or Session Cookie) (SEC-1, SEC-3)
		api.Group(func(protected chi.Router) {
			protected.Use(s.authMiddleware)

			// Dashboard Overview route (UI-1)
			protected.Get("/overview", s.handleGetOverview)

			// Project management routes (PRJ-1..8)
			protected.Get("/projects", s.handleListProjects)
			protected.Post("/projects", s.handleCreateProject)
			protected.Get("/projects/{id}", s.handleGetProject)
			protected.Post("/projects/{id}/config-template", s.handleCreateProjectConfigTemplate)
			protected.Post("/projects/config-template", s.handleCreateProjectConfigTemplate)

			// Job lifecycle routes (INT-1, PIP-6, PIP-7)
			protected.Get("/jobs", s.handleListJobs)
			protected.Post("/jobs", s.handleCreateJob)
			protected.Get("/jobs/{id}", s.handleGetJob)
			protected.Get("/jobs/{id}/steps", s.handleGetJobSteps)
			protected.Post("/jobs/{id}/cancel", s.handleCancelJob)
			protected.Post("/jobs/{id}/retry", s.handleRetryJob)
			protected.Post("/jobs/{id}/clarification", s.handleClarification)
			protected.Get("/jobs/{id}/evidence", s.handleGetEvidence)
			protected.Get("/jobs/{id}/diff", s.handleGetDiff)
			protected.Get("/jobs/{id}/artifacts/{name}", s.handleGetArtifact)

			// SSE Live Events Stream (LOG-4)
			protected.Get("/events", s.handleEventsSSE)

			// Interactive session-only routes (SEC-4, APR-7, PRJ-8):
			// Approvals, rejections, and project archiving strictly require an interactive browser cookie.
			protected.Group(func(sessionOnly chi.Router) {
				sessionOnly.Use(s.requireSessionOnlyMiddleware)
				sessionOnly.Post("/jobs/{id}/approve", s.handleApproveJob)
				sessionOnly.Post("/jobs/{id}/reject", s.handleRejectJob)
				sessionOnly.Post("/projects/{id}/archive", s.handleArchiveProject)
			})
		})
	})

	// Static UI assets and SPA client-side routing fallback
	if uiFS != nil {
		s.setupStaticFiles(r)
	}

	s.Router = r
	return s
}

// hostHeaderMiddleware validates that incoming requests use a loopback Host header (SEC-2).
// This prevents DNS rebinding attacks from malicious websites in the user's browser.
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
		// API endpoints that don't match must return 404 JSON, never SPA HTML
		if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/api" {
			http.NotFound(w, r)
			return
		}

		// Try opening the requested file in the embedded UI filesystem
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

		// File does not exist on disk/embed: serve index.html for client-side routing
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

// Start begins listening and serving HTTP requests on the specified network address.
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

// Shutdown gracefully stops the HTTP server, allowing active requests to finish.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.httpServer != nil {
		return s.httpServer.Shutdown(ctx)
	}
	return nil
}
