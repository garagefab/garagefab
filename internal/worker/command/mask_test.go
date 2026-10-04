// Package command_test provides tests for command execution and masking.
//
// ==============================================================================
// ARCHITECTURAL ROLE & SECURITY VERIFICATION:
// Output Sanitization & Credential Masking Tests (LOG-6).
//
// Verifies requirement LOG-6:
// If GH_TOKEN or GITHUB_TOKEN is set in the host environment, its literal value
// is masked with "***" wherever it appears in stdout/stderr output lines or tail buffers.
// ==============================================================================
package command_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/garagefab/garagefab/internal/worker/command"
)

// TestMasking_GhTokens_LOG6 verifies requirement LOG-6:
// Output sanitizer replaces GH_TOKEN and GITHUB_TOKEN values with '***'.
func TestMasking_GhTokens_LOG6(t *testing.T) {
	// Set mock tokens in environment
	t.Setenv("GH_TOKEN", "ghp_secretToken12345")
	t.Setenv("GITHUB_TOKEN", "ghs_anotherSecretToken67890")

	input := "Connecting with token ghp_secretToken12345 and backup ghs_anotherSecretToken67890..."
	expected := "Connecting with token *** and backup ***..."

	masked := command.MaskTokens(input)
	if masked != expected {
		t.Errorf("expected %q, got %q", expected, masked)
	}

	// Verify unaffected input remains unchanged
	cleanInput := "Normal compilation output without tokens"
	if command.MaskTokens(cleanInput) != cleanInput {
		t.Errorf("expected clean input to remain unchanged, got %q", command.MaskTokens(cleanInput))
	}
}

// TestMasking_StoredCommandLog_LOG6 verifies that GH_TOKEN/GITHUB_TOKEN are masked both in the
// returned command result and in the stored log file (LOG-6).
func TestMasking_StoredCommandLog_LOG6(t *testing.T) {
	t.Setenv("GH_TOKEN", "ghp_secretToken12345")
	t.Setenv("GITHUB_TOKEN", "ghs_anotherSecretToken67890")

	dir := t.TempDir()
	logPath := filepath.Join(dir, "step.log")
	runner := command.NewRunner()
	res, err := runner.Run(context.Background(), command.RunOptions{
		WorkDir: dir,
		Command: "echo 'using ghp_secretToken12345 and ghs_anotherSecretToken67890'",
		LogPath: logPath,
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if strings.Contains(res.Stdout, "ghp_secretToken12345") || strings.Contains(res.Stdout, "ghs_anotherSecretToken67890") {
		t.Fatalf("token leaked into command output: %q", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "***") {
		t.Fatalf("expected masked output, got: %q", res.Stdout)
	}

	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log failed: %v", err)
	}
	if strings.Contains(string(logData), "ghp_secretToken12345") || strings.Contains(string(logData), "ghs_anotherSecretToken67890") {
		t.Fatalf("token leaked into stored log: %q", string(logData))
	}
	if !strings.Contains(string(logData), "***") {
		t.Fatalf("expected masked stored log, got: %q", string(logData))
	}
}
