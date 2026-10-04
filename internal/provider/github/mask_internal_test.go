// Package github verifies credential redaction for the gh CLI runner (LOG-6).
package github

import (
	"strings"
	"testing"
)

// TestSanitizeOutput_MasksTokens_LOG6 verifies that GH_TOKEN/GITHUB_TOKEN are redacted from
// captured CLI stderr before it is surfaced in errors or logs (LOG-6).
func TestSanitizeOutput_MasksTokens_LOG6(t *testing.T) {
	t.Setenv("GH_TOKEN", "ghp_secretToken12345")
	t.Setenv("GITHUB_TOKEN", "ghs_anotherSecretToken67890")

	out := sanitizeOutput("error: bad credentials ghp_secretToken12345 / ghs_anotherSecretToken67890")
	if strings.Contains(out, "ghp_secretToken12345") || strings.Contains(out, "ghs_anotherSecretToken67890") {
		t.Fatalf("token leaked into sanitized output: %q", out)
	}
	if !strings.Contains(out, "***") {
		t.Fatalf("expected masked output, got %q", out)
	}
}
