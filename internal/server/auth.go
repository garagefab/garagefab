// Package server implements authentication middleware, session cookies, and CSRF protection.
//
// ==============================================================================
// ARCHITECTURAL ROLE & SECURITY DESIGN:
// Dual-Mode Authentication & Interactive Session Guardrails (SEC-1..5, APR-7).
//
// 1. Dual-Mode Authentication:
//
//   - CLI / Automations: Authenticate via HTTP Header `Authorization: Bearer <api_token>`.
//
//   - Web Dashboard: Authenticates via HttpOnly, SameSite=Strict cookie (`gf_session`).
//
//     2. Interactive Session-Only Guardrail (SEC-4, APR-7):
//     Gate approval (`/api/jobs/{id}/approve`) and rejection (`/reject`) endpoints strictly
//     require an interactive session cookie (`AuthTypeSession`). Bearer token authentication
//     is forbidden on these endpoints.
//     Why? To prevent an automated background script, agent runner subprocess, or GitHub
//     action from bypassing the human gate and auto-approving its own code changes.
//
//     3. CSRF Protection for Cookie Requests (SEC-5):
//     Any mutating HTTP request (POST, PUT, DELETE) authenticated via cookie must have an
//     `Origin` or `Referer` header originating from loopback (localhost or 127.0.0.1).
//
// GO CONCEPTS & JAVA / SPRING SECURITY COMPARISON:
//
//  1. Request Context Propagation (`context.WithValue`):
//     In Java/Spring Security: The authenticated user principal is stored in `SecurityContextHolder`
//     (backed by a ThreadLocal).
//     In Go: Request-scoped data is stored directly in the `http.Request` context (`r.Context()`).
//     Using a private unexported type `type contextKey string` guarantees zero key collisions
//     with other packages.
//
// ==============================================================================
package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/garagefab/garagefab/internal/store"
)

// Private context key type to prevent collision with other middleware in context.
type contextKey string

const authContextKey contextKey = "authContext"

// AuthType represents the method used to authenticate the current HTTP request.
type AuthType string

const (
	AuthTypeBearer  AuthType = "bearer"  // Authenticated via Authorization: Bearer <token>
	AuthTypeSession AuthType = "session" // Authenticated via gf_session browser cookie
)

// AuthInfo holds the authentication metadata for the current request.
type AuthInfo struct {
	Type    AuthType       // Authentication method
	Session *store.Session // Associated database session (nil if Bearer token)
}

// getAuthInfo extracts the AuthInfo object from the request context.
func getAuthInfo(ctx context.Context) *AuthInfo {
	if val, ok := ctx.Value(authContextKey).(*AuthInfo); ok {
		return val
	}
	return nil
}

// computeTokenHash generates a SHA-256 hash of the API token.
func computeTokenHash(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// generateSessionID generates a cryptographically secure 32-byte hex string (256-bit entropy).
func generateSessionID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// constantTimeEquals securely compares two secret strings in constant time (SEC-3).
// This mitigates timing side-channel attacks on secret tokens and hashes.
func constantTimeEquals(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// authMiddleware enforces Bearer or Session cookie authentication (SEC-3, SEC-5).
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1. Check for Bearer token in Authorization header
		authHeader := r.Header.Get("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			token := strings.TrimPrefix(authHeader, "Bearer ")
			if constantTimeEquals(token, s.Config.Server.APIToken) {
				// Inject Bearer AuthInfo into request context
				ctx := context.WithValue(r.Context(), authContextKey, &AuthInfo{Type: AuthTypeBearer})
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
		}

		// 2. Check for Session cookie (gf_session)
		cookie, err := r.Cookie("gf_session")
		if err == nil && cookie.Value != "" {
			session, err := s.DB.Sessions().GetSession(r.Context(), cookie.Value)
			if err == nil && session != nil {
				// Verify session expiration and token hash validity
				expectedHash := computeTokenHash(s.Config.Server.APIToken)
				if session.ExpiresAt.After(time.Now()) && constantTimeEquals(session.TokenHash, expectedHash) {
					// CSRF protection for cookie auth (SEC-5): verify Origin/Referer on mutating methods
					if r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodDelete {
						if !isValidOrigin(r) {
							http.Error(w, "Forbidden: invalid Origin or Referer header", http.StatusForbidden)
							return
						}
					}

					// Inject Session AuthInfo into request context
					ctx := context.WithValue(r.Context(), authContextKey, &AuthInfo{
						Type:    AuthTypeSession,
						Session: session,
					})
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
			}
		}

		// Neither authentication method succeeded: return 401 Unauthorized
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
	})
}

// requireSessionOnlyMiddleware restricts endpoints to Session cookie only (SEC-4, APR-7).
// Rejects Bearer token authentication even if valid.
func (s *Server) requireSessionOnlyMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := getAuthInfo(r.Context())
		if auth == nil || auth.Type != AuthTypeSession {
			http.Error(w, "Forbidden: action requires interactive session", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// isValidOrigin checks whether the Origin or Referer header points to loopback (SEC-5).
func isValidOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		origin = r.Header.Get("Referer")
	}
	if origin == "" {
		return true // Allow requests without Origin/Referer if same-origin non-browser
	}

	u, err := url.Parse(origin)
	if err != nil {
		return false
	}

	host := u.Hostname()
	return host == "127.0.0.1" || host == "localhost"
}

// handleCreateSession handles POST /api/session (SEC-3).
// Exchanges the API token for an HttpOnly session cookie.
// Accepts the API token via JSON body ({"token": "..."}), form body ("token=..."),
// or HTTP Authorization header ("Bearer <token>").
func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var token string

	// 1. Try decoding JSON payload if Content-Type is application/json
	if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		var req struct {
			Token string `json:"token"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		token = req.Token
	}

	// 2. Fall back to form-encoded body or query parameter
	if token == "" {
		token = r.FormValue("token")
	}

	// 3. Fall back to Authorization Bearer header
	if token == "" {
		authHeader := r.Header.Get("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			token = strings.TrimPrefix(authHeader, "Bearer ")
		}
	}

	if !constantTimeEquals(token, s.Config.Server.APIToken) {
		http.Error(w, "Invalid API token", http.StatusUnauthorized)
		return
	}

	// Generate secure random session ID
	sessionID, err := generateSessionID()
	if err != nil {
		http.Error(w, "Failed to generate session", http.StatusInternalServerError)
		return
	}

	session := &store.Session{
		ID:        sessionID,
		TokenHash: computeTokenHash(s.Config.Server.APIToken),
		ExpiresAt: time.Now().Add(7 * 24 * time.Hour), // 7-day session lifetime
	}

	if err := s.DB.Sessions().CreateSession(r.Context(), session); err != nil {
		http.Error(w, "Failed to persist session", http.StatusInternalServerError)
		return
	}

	// Set session cookie: HttpOnly (unreachable by JS), SameSite=Strict (no cross-site leak) (SEC-3)
	http.SetCookie(w, &http.Cookie{
		Name:     "gf_session",
		Value:    sessionID,
		Path:     "/",
		Expires:  session.ExpiresAt,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}
