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
