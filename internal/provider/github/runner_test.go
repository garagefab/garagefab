// Package github_test provides tests for runner execution and error mapping.
//
// ==============================================================================
// ARCHITECTURAL ROLE & ERROR MAPPING TESTS:
// Runner Error Translation & Masking Unit Tests (GHB-1, LOG-6).
// ==============================================================================
package github_test

import (
	"context"
	"errors"
	"testing"

	"github.com/garagefab/garagefab/internal/provider/github"
)

// TestRunner_ErrorTranslation verifies that CLI error scenarios are mapped
// to the appropriate typed sentinel errors.
func TestRunner_ErrorTranslation(t *testing.T) {
	ctx := context.Background()

	// 1. Non-existent binary maps to ErrGHNotInstalled
	r := github.NewDefaultGHRunner("non-existent-gh-binary-12345")
	_, err := r.Run(ctx, nil, "--version")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, github.ErrGHNotInstalled) {
		t.Fatalf("expected ErrGHNotInstalled, got: %v", err)
	}
}
