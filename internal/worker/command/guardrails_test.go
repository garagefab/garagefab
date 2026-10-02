// Package command_test contains unit and integration tests for subprocess guardrails.
//
// ==============================================================================
// ARCHITECTURAL ROLE & PATTERNS:
// Unit and Integration Test for Guardrails (GRD-1, GRD-2, GRD-4).
//
// Tests both pure pattern matching and real Git repository diff inspection
// to ensure modified/deleted protected files are flagged while new files pass.
// ==============================================================================
package command_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/garagefab/garagefab/internal/worker/command"
)

func TestMatchPathPattern(t *testing.T) {
	tests := []struct {
		pattern  string
		path     string
		expected bool
	}{
		{"**/*_test.go", "auth_test.go", true},
		{"**/*_test.go", "pkg/auth_test.go", true},
		{"**/*_test.go", "pkg/deep/nested/feature_test.go", true},
		{"**/*_test.go", "main.go", false},
		{"**/*_test.go", "pkg/test_helper.go", false},
		{"internal/**", "internal/factory/pipeline.go", true},
		{"internal/**", "cmd/garagefab/main.go", false},
		{"Makefile", "Makefile", true},
		{"Makefile", "other/Makefile", false},
		{"*.md", "README.md", true},
		{"*.md", "docs/spec.md", false},
	}

	for _, tc := range tests {
		t.Run(tc.pattern+"_"+tc.path, func(t *testing.T) {
			actual := command.MatchPathPattern(tc.pattern, tc.path)
			if actual != tc.expected {
				t.Fatalf("pattern %q on path %q: expected %v, got %v", tc.pattern, tc.path, tc.expected, actual)
			}
		})
	}
}

// TestCheckProtectedPaths_GRD1 verifies that modifying or deleting existing protected files
// triggers a guardrail violation, whereas creating new test files is allowed (GRD-1).
func TestCheckProtectedPaths_GRD1(t *testing.T) {
	ctx := context.Background()
	repoDir := t.TempDir()

	// Initialize git repository
	runGit := func(args ...string) string {
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repoDir}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %s (%v)", args, string(out), err)
		}
		return strings.TrimSpace(string(out))
	}

	runGit("init")
	runGit("config", "user.name", "Tester")
	runGit("config", "user.email", "tester@garagefab.local")

	// Create initial tracked files
	existingTest := filepath.Join(repoDir, "service_test.go")
	if err := os.WriteFile(existingTest, []byte("package main\n// test 1"), 0644); err != nil {
		t.Fatal(err)
	}
	existingMain := filepath.Join(repoDir, "main.go")
	if err := os.WriteFile(existingMain, []byte("package main\nfunc main() {}"), 0644); err != nil {
		t.Fatal(err)
	}

	runGit("add", ".")
	runGit("commit", "-m", "initial commit")
	baseSHA := runGit("rev-parse", "HEAD")

	protectedPatterns := []string{"**/*_test.go"}

	// Case 1: Modifying main.go (unprotected) -> should pass
	if err := os.WriteFile(existingMain, []byte("package main\n// edited"), 0644); err != nil {
		t.Fatal(err)
	}
	violations, err := command.CheckProtectedPaths(ctx, repoDir, baseSHA, protectedPatterns)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected 0 violations for main.go, got %v", violations)
	}

	// Case 2: Adding a NEW test file (GRD-1: new files allowed) -> should pass
	newTest := filepath.Join(repoDir, "new_feature_test.go")
	if err := os.WriteFile(newTest, []byte("package main\n// new test"), 0644); err != nil {
		t.Fatal(err)
	}
	violations, err = command.CheckProtectedPaths(ctx, repoDir, baseSHA, protectedPatterns)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected 0 violations for new test file, got %v", violations)
	}

	// Case 3: Modifying existing service_test.go -> MUST trigger violation
	if err := os.WriteFile(existingTest, []byte("package main\n// modified test"), 0644); err != nil {
		t.Fatal(err)
	}
	violations, err = command.CheckProtectedPaths(ctx, repoDir, baseSHA, protectedPatterns)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation for modified service_test.go, got %d: %v", len(violations), violations)
	}
	if violations[0].Path != "service_test.go" {
		t.Fatalf("expected violation path service_test.go, got %s", violations[0].Path)
	}

	// Revert service_test.go
	runGit("checkout", "service_test.go")

	// Case 4: Deleting existing service_test.go -> MUST trigger violation
	if err := os.Remove(existingTest); err != nil {
		t.Fatal(err)
	}
	violations, err = command.CheckProtectedPaths(ctx, repoDir, baseSHA, protectedPatterns)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation for deleted service_test.go, got %d: %v", len(violations), violations)
	}
}
