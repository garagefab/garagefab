package server_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/garagefab/garagefab/internal/config"
	"github.com/garagefab/garagefab/internal/server"
)

func TestHealthEndpoint_CLI1(t *testing.T) {
	cfg := config.Default()
	cfg.Server.Listen = "127.0.0.1:7878"

	srv := server.NewServer(cfg, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	req.Host = "127.0.0.1:7878"
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read response body: %v", err)
	}

	var data map[string]string
	if err := json.Unmarshal(body, &data); err != nil {
		t.Fatalf("failed to parse json response: %v", err)
	}

	if data["status"] != "ok" {
		t.Errorf("expected status 'ok', got %q", data["status"])
	}
}

func TestHostHeaderCheck_SEC2(t *testing.T) {
	cfg := config.Default()
	cfg.Server.Listen = "127.0.0.1:7878"

	srv := server.NewServer(cfg, nil, nil)

	// Disallowed Host header
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	req.Host = "attacker.example.com"
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden for external host, got %d", resp.StatusCode)
	}

	// Allowed localhost Host header
	req2 := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	req2.Host = "localhost:7878"
	w2 := httptest.NewRecorder()

	srv.Router.ServeHTTP(w2, req2)
	resp2 := w2.Result()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK for localhost, got %d", resp2.StatusCode)
	}
}

func TestSPAFallback(t *testing.T) {
	cfg := config.Default()
	cfg.Server.Listen = "127.0.0.1:7878"

	mockFS := fstest.MapFS{
		"index.html":    {Data: []byte("<html><body>Dashboard</body></html>")},
		"assets/app.js": {Data: []byte("console.log('app')")},
	}

	srv := server.NewServer(cfg, nil, mockFS)

	// Root path should serve index.html
	req1 := httptest.NewRequest(http.MethodGet, "/", nil)
	req1.Host = "127.0.0.1:7878"
	w1 := httptest.NewRecorder()
	srv.Router.ServeHTTP(w1, req1)
	if body := w1.Body.String(); body != "<html><body>Dashboard</body></html>" {
		t.Errorf("expected index.html, got %q", body)
	}

	// Unknown non-api route should fall back to index.html (SPA routing)
	req2 := httptest.NewRequest(http.MethodGet, "/projects/1/jobs/2", nil)
	req2.Host = "127.0.0.1:7878"
	w2 := httptest.NewRecorder()
	srv.Router.ServeHTTP(w2, req2)
	if body := w2.Body.String(); body != "<html><body>Dashboard</body></html>" {
		t.Errorf("expected SPA fallback to index.html, got %q", body)
	}

	// Existing static asset
	req3 := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	req3.Host = "127.0.0.1:7878"
	w3 := httptest.NewRecorder()
	srv.Router.ServeHTTP(w3, req3)
	if body := w3.Body.String(); body != "console.log('app')" {
		t.Errorf("expected asset app.js, got %q", body)
	}

	// Unknown API route should 404, not fallback
	req4 := httptest.NewRequest(http.MethodGet, "/api/unknown", nil)
	req4.Host = "127.0.0.1:7878"
	w4 := httptest.NewRecorder()
	srv.Router.ServeHTTP(w4, req4)
	if w4.Code != http.StatusNotFound {
		t.Errorf("expected 404 for unknown api route, got %d", w4.Code)
	}
}
