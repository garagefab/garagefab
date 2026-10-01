package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/garagefab/garagefab/internal/store"
)

type contextKey string

const authContextKey contextKey = "authContext"

// AuthType represents the method used to authenticate.
type AuthType string

const (
	AuthTypeBearer  AuthType = "bearer"
	AuthTypeSession AuthType = "session"
)

// AuthInfo holds the authentication metadata for the current request.
type AuthInfo struct {
	Type    AuthType
	Session *store.Session
}

func getAuthInfo(ctx context.Context) *AuthInfo {
	if val, ok := ctx.Value(authContextKey).(*AuthInfo); ok {
		return val
	}
	return nil
}

// computeTokenHash generates SHA-256 of the API token.
func computeTokenHash(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// generateSessionID generates a cryptographically secure 32-byte hex string.
func generateSessionID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// authMiddleware enforces Bearer or Session cookie authentication (SEC-3, SEC-5).
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1. Try Bearer token in Authorization header
		authHeader := r.Header.Get("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			token := strings.TrimPrefix(authHeader, "Bearer ")
			if token == s.Config.Server.APIToken && token != "" {
				ctx := context.WithValue(r.Context(), authContextKey, &AuthInfo{Type: AuthTypeBearer})
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
		}

		// 2. Try Session cookie
		cookie, err := r.Cookie("gf_session")
		if err == nil && cookie.Value != "" {
			session, err := s.DB.Sessions().GetSession(r.Context(), cookie.Value)
			if err == nil && session != nil {
				// Validate expiration and token hash
				expectedHash := computeTokenHash(s.Config.Server.APIToken)
				if session.ExpiresAt.After(time.Now()) && session.TokenHash == expectedHash {
					// CSRF protection for cookie auth (SEC-5): verify Origin/Referer on mutating methods
					if r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodDelete {
						if !isValidOrigin(r) {
							http.Error(w, "Forbidden: invalid Origin or Referer header", http.StatusForbidden)
							return
						}
					}

					ctx := context.WithValue(r.Context(), authContextKey, &AuthInfo{
						Type:    AuthTypeSession,
						Session: session,
					})
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
			}
		}

		http.Error(w, "Unauthorized", http.StatusUnauthorized)
	})
}

// requireSessionOnlyMiddleware restricts endpoints to Session cookie only (SEC-4, APR-7).
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
func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	token := r.FormValue("token")
	if token == "" {
		// Try reading token from Authorization header or bearer
		authHeader := r.Header.Get("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			token = strings.TrimPrefix(authHeader, "Bearer ")
		}
	}

	if token != s.Config.Server.APIToken || token == "" {
		http.Error(w, "Invalid API token", http.StatusUnauthorized)
		return
	}

	sessionID, err := generateSessionID()
	if err != nil {
		http.Error(w, "Failed to generate session", http.StatusInternalServerError)
		return
	}

	session := &store.Session{
		ID:        sessionID,
		TokenHash: computeTokenHash(s.Config.Server.APIToken),
		ExpiresAt: time.Now().Add(7 * 24 * time.Hour), // 7 days
	}

	if err := s.DB.Sessions().CreateSession(r.Context(), session); err != nil {
		http.Error(w, "Failed to persist session", http.StatusInternalServerError)
		return
	}

	// Set session cookie: HttpOnly, SameSite=Strict, Path=/ (SEC-3)
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
