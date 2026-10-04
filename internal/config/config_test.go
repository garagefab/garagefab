// Package config_test contains unit tests for configuration loading and validation.
//
// ==============================================================================
// GO TESTING CONCEPTS & LOGGING CAPTURE:
//
//  1. Testing File Permissions (`os.Stat` and `info.Mode().Perm()`):
//     Go provides direct access to POSIX permission bits via `os.FileMode`.
//     Using `%#o` formatting prints octal notation (e.g., 0700 or 0600).
//
//  2. Intercepting Structured Logs in Unit Tests:
//     In Java/Logback, testing log output often requires custom appenders or ListAppender.
//     In Go with `log/slog`, we can plug a `bytes.Buffer` into `slog.NewTextHandler`
//     and replace the default logger with `slog.SetDefault`.
//     Using `defer slog.SetDefault(origLogger)` ensures the logger is restored after the test.
//
// ==============================================================================
package config_test

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/garagefab/garagefab/internal/config"
)

// TestLoad_NewDataDir_CLI6 verifies requirement CLI-6:
// If Garagefab is started with a non-existent data directory, it initializes the directory
// with mode 0700, generates a config.yaml with mode 0600, and sets a 64-character hex API token.
func TestLoad_NewDataDir_CLI6(t *testing.T) {
	tempParent := t.TempDir()
	dataDir := filepath.Join(tempParent, "nonexistent-dir")

	cfg, err := config.Load(dataDir)
	if err != nil {
		t.Fatalf("Load returned unexpected error: %v", err)
	}

	// Verify directory permissions: mode 0700 (owner only)
	info, err := os.Stat(dataDir)
	if err != nil {
		t.Fatalf("failed to stat dataDir: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0700 {
		t.Errorf("expected directory mode 0700, got %#o", mode)
	}

	// Verify config file permissions: mode 0600 (owner read/write only)
	configFile := filepath.Join(dataDir, "config.yaml")
	fileInfo, err := os.Stat(configFile)
	if err != nil {
		t.Fatalf("failed to stat config.yaml: %v", err)
	}
	if mode := fileInfo.Mode().Perm(); mode != 0600 {
		t.Errorf("expected config file mode 0600, got %#o", mode)
	}

	// Verify default configuration properties
	if cfg.Server.Listen != "127.0.0.1:7878" {
		t.Errorf("expected Server.Listen '127.0.0.1:7878', got %q", cfg.Server.Listen)
	}
	// 32 random bytes hex-encoded = 64 hexadecimal characters
	if len(cfg.Server.APIToken) != 64 {
		t.Errorf("expected generated APIToken to be 64 hex chars, got len %d: %q", len(cfg.Server.APIToken), cfg.Server.APIToken)
	}
	if cfg.Engine.MaxConcurrentJobs != 5 {
		t.Errorf("expected Engine.MaxConcurrentJobs=5, got %d", cfg.Engine.MaxConcurrentJobs)
	}
	if cfg.Engine.MaxRepairAttempts != 3 {
		t.Errorf("expected Engine.MaxRepairAttempts=3, got %d", cfg.Engine.MaxRepairAttempts)
	}
	if cfg.DataDir != dataDir {
		t.Errorf("expected DataDir %q, got %q", dataDir, cfg.DataDir)
	}
}

// TestLoad_NonLoopbackListen_Rejected verifies requirement SEC-1:
// Binding to non-loopback interfaces (e.g. 0.0.0.0) is rejected for security.
func TestLoad_NonLoopbackListen_Rejected(t *testing.T) {
	dataDir := t.TempDir()
	configFile := filepath.Join(dataDir, "config.yaml")
	content := `
server:
  listen: "0.0.0.0:7878"
  api_token: "secret123"
`
	if err := os.WriteFile(configFile, []byte(content), 0600); err != nil {
		t.Fatalf("failed to write config.yaml: %v", err)
	}

	_, err := config.Load(dataDir)
	if err == nil {
		t.Fatal("expected error for non-loopback listen address, got nil")
	}
	if !strings.Contains(err.Error(), "server.listen") {
		t.Errorf("expected error message to mention 'server.listen', got %v", err)
	}
}

// TestLoad_EmptyAPIToken_Rejected verifies that a blank API token fails validation.
func TestLoad_EmptyAPIToken_Rejected(t *testing.T) {
	dataDir := t.TempDir()
	configFile := filepath.Join(dataDir, "config.yaml")
	content := `
server:
  listen: "127.0.0.1:7878"
  api_token: ""
`
	if err := os.WriteFile(configFile, []byte(content), 0600); err != nil {
		t.Fatalf("failed to write config.yaml: %v", err)
	}

	_, err := config.Load(dataDir)
	if err == nil {
		t.Fatal("expected error for empty api_token, got nil")
	}
	if !strings.Contains(err.Error(), "api_token") {
		t.Errorf("expected error message to mention 'api_token', got %v", err)
	}
}

// TestLoad_InsecurePermissions_SEC7 verifies requirement SEC-7:
// If config files have permissions that are too permissive, a warning log is emitted.
func TestLoad_InsecurePermissions_SEC7(t *testing.T) {
	tempParent := t.TempDir()
	dataDir := filepath.Join(tempParent, "insecure-dir")
	if err := os.Mkdir(dataDir, 0755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}
	configFile := filepath.Join(dataDir, "config.yaml")
	content := `
server:
  listen: "127.0.0.1:7878"
  api_token: "valid-token-12345"
`
	if err := os.WriteFile(configFile, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write config.yaml: %v", err)
	}

	// Capture slog warning output into an in-memory buffer
	var logBuf bytes.Buffer
	handler := slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelWarn})
	origLogger := slog.Default()
	slog.SetDefault(slog.New(handler))
	defer slog.SetDefault(origLogger) // Ensure logger is restored after test ends

	cfg, err := config.Load(dataDir)
	if err != nil {
		t.Fatalf("Load should succeed despite warnings, got: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}

	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "permission") && !strings.Contains(logOutput, "mode") {
		t.Errorf("expected warning log about permissions, got: %s", logOutput)
	}
}
