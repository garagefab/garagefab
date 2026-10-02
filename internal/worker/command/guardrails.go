// Package command provides secure, monitored subprocess execution inside worktrees.
//
// ==============================================================================
// ARCHITECTURAL ROLE & PATTERNS:
// Infrastructure Security Guardrails / Policy Verification (GRD-1..4).
//
//  1. Architectural Role:
//     This file implements the workspace integrity guardrails specified in GRD-1..4:
//     - Protected Paths: Ensures that AI agents do not modify, rename, or delete existing
//     test files or critical infrastructure files (e.g. `**/*_test.go`).
//     - New File Permission: Explicitly allows agents to create new files matching the pattern
//     so they can add new test suites while being barred from weakening existing ones.
//     - Custom Command Guardrails: Executes custom verification scripts (GRD-3).
//
//  2. Enterprise / Java Comparison:
//     Analogous to Maven Enforcer Plugin (`maven-enforcer-plugin`), ArchUnit rules, or
//     Git pre-commit/pre-push hooks in enterprise Java/Kotlin repositories.
//     Instead of relying on the agent's self-restraint or prompt compliance, these
//     guardrails act as an external, programmatic security envelope.
//
//  3. Go Idioms:
//     - Path Normalization: Always convert OS-specific separators (`\` on Windows)
//     to forward slashes `/` via `filepath.ToSlash` for cross-platform glob matching.
//     - Zero External Dependencies: Pure Go implementation for recursive directory
//     glob matching (`**`) without pulling in heavy regex or third-party AST packages.
//
// ==============================================================================
package command

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// MatchPathPattern tests whether a repository-relative path matches a glob pattern.
// Supports standard filepath globs as well as double-star `**` for arbitrary directory depth.
// Examples:
//   - `**/*_test.go` matches `auth_test.go`, `pkg/auth_test.go`, `pkg/sub/auth_test.go`
//   - `internal/**` matches all files under `internal/`
//   - `Makefile` matches only `Makefile` in repo root
func MatchPathPattern(pattern, path string) bool {
	normPattern := filepath.ToSlash(filepath.Clean(pattern))
	normPath := filepath.ToSlash(filepath.Clean(path))

	// Exact string match
	if normPattern == normPath {
		return true
	}

	// Handle leading `**/` (e.g. `**/*_test.go` or `**/secret.json`)
	if strings.HasPrefix(normPattern, "**/") {
		subPattern := normPattern[3:]

		// 1. Direct match on filename
		if ok, _ := filepath.Match(subPattern, filepath.Base(normPath)); ok {
			return true
		}

		// 2. Match on the full relative path
		if ok, _ := filepath.Match(subPattern, normPath); ok {
			return true
		}

		// 3. Match against any subpath segment suffix (e.g. sub/dir/foo_test.go)
		parts := strings.Split(normPath, "/")
		for i := 0; i < len(parts); i++ {
			subPath := strings.Join(parts[i:], "/")
			if ok, _ := filepath.Match(subPattern, subPath); ok {
				return true
			}
		}
	}

	// Handle trailing `/**` (e.g. `internal/**`)
	if strings.HasSuffix(normPattern, "/**") {
		prefix := normPattern[:len(normPattern)-3]
		if normPath == prefix || strings.HasPrefix(normPath, prefix+"/") {
			return true
		}
	}

	// Standard glob match (e.g. `*.go`, `docs/*.md`)
	matched, err := filepath.Match(normPattern, normPath)
	return err == nil && matched
}

// GuardrailViolation represents a protected path that was tampered with by an agent.
type GuardrailViolation struct {
	Path   string // Relative file path in the repository
	Status string // Git diff status code (M = Modified, D = Deleted, R = Renamed)
}

// CheckProtectedPaths checks all changes in workDir against stepStartSHA.
// Returns a list of violating file paths that matched protected_paths globs (GRD-1).
// Existing files that were Modified (M), Deleted (D), or Renamed (R) constitute a violation.
// Newly Added files (A) are explicitly permitted and never trigger a violation.
func CheckProtectedPaths(ctx context.Context, workDir, stepStartSHA string, protectedPatterns []string) ([]GuardrailViolation, error) {
	if len(protectedPatterns) == 0 || stepStartSHA == "" {
		return nil, nil
	}

	// Query git for name and change status relative to the step start commit
	cmd := exec.CommandContext(ctx, "git", "-C", workDir, "diff", "--name-status", stepStartSHA)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("guardrails: git diff --name-status: %w", err)
	}

	var violations []GuardrailViolation
	scanner := bufio.NewScanner(strings.NewReader(string(out)))

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		// Fields in git diff --name-status are tab-separated:
		// e.g. "M\tpath/to/file.go" or "R100\told/path\tnew/path"
		parts := strings.Split(line, "\t")
		if len(parts) < 2 {
			continue
		}

		statusCode := parts[0]
		// Status letter: M (Modified), D (Deleted), R (Renamed), A (Added)
		action := statusCode[:1]

		// Requirement GRD-1: New files are permitted
		if action == "A" {
			continue
		}

		// For modified, deleted, or renamed existing files:
		filesToCheck := parts[1:]
		for _, f := range filesToCheck {
			f = filepath.ToSlash(filepath.Clean(f))
			for _, pattern := range protectedPatterns {
				if MatchPathPattern(pattern, f) {
					violations = append(violations, GuardrailViolation{
						Path:   f,
						Status: action,
					})
					break
				}
			}
		}
	}

	return violations, nil
}
