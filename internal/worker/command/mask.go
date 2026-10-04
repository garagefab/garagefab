// Package command provides shell command execution, subprocess management,
// environment sanitization, and output masking for Garagefab workers.
//
// ==============================================================================
// ARCHITECTURAL ROLE & PATTERNS:
// Subprocess Output Masking & Secret Sanitization (Hexagonal Worker Adapter).
//
// In Clean / Hexagonal Architecture:
// `mask.go` belongs to the driven worker infrastructure adapter. It ensures that
// sensitive credentials (specifically GitHub personal access tokens or CLI auth tokens)
// present in the host environment never leak into persistent log files, UI activity
// feeds, or error summaries (LOG-6).
//
// ENTERPRISE / JAVA SPRING COMPARISON:
//   - Logback / Log4j Converter: In Java Spring Boot applications, sensitive tokens
//     in logs are sanitized using custom layout masking converters or Jackson serializers.
//   - In Go: Output streaming pipelines (`proc.go` and `runner.go`) apply `MaskTokens`
//     synchronously to each scanned line before writing to log files or memory buffers.
//
// GO IDIOMS & CONCEPTS:
//  1. Environment-driven token registry:
//     Reads `GH_TOKEN` and `GITHUB_TOKEN` from `os.Getenv`. If non-empty, replaces
//     all occurrences with `***`.
//  2. In-place string substitution:
//     Uses `strings.ReplaceAll` for deterministic, fast token redaction.
//
// ==============================================================================
package command

import (
	"os"
	"strings"
)

// MaskTokens replaces occurrences of GH_TOKEN and GITHUB_TOKEN in the provided text with "***" (LOG-6).
// If neither token is set in the host environment, it returns the text unmodified.
func MaskTokens(text string) string {
	if ghToken := os.Getenv("GH_TOKEN"); ghToken != "" {
		text = strings.ReplaceAll(text, ghToken, "***")
	}
	if githubToken := os.Getenv("GITHUB_TOKEN"); githubToken != "" {
		text = strings.ReplaceAll(text, githubToken, "***")
	}
	return text
}
