// Package main contains tests for CLI commands in the main package.
//
// ==============================================================================
// GO TESTING CONCEPTS & JAVA COMPARISON:
//
//  1. `t.TempDir()`:
//     Go standard library provides `t.TempDir()` starting in Go 1.15.
//     In Java/JUnit 5, this is equivalent to `@TempDir Path tempDir`.
//     The directory is guaranteed to be unique and is automatically cleaned up when
//     the test finishes, even if the test panics.
//
//  2. Testing CLI Commands (Cobra I/O redirection):
//     In Java, redirecting `System.out` requires `System.setOut(new PrintStream(baos))`
//     and restoring it in `@AfterEach`.
//     In Go with Cobra, commands support `cmd.SetOut(buf)` where `buf` is a `*bytes.Buffer`,
//     allowing clean, concurrent, in-memory capture of stdout.
//
//  3. Defer for Cleanup:
//     `defer func() { dataDirFlag = "" }()` ensures that package-level flag state
//     is restored regardless of whether assertions pass or fail (like `finally` in Java).
//
// ==============================================================================
package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/garagefab/garagefab/internal/config"
	"github.com/garagefab/garagefab/internal/store"
)

// TestCLI_Status_CLI4 verifies requirement CLI-4:
// When `garagefab status` is executed, it reads the local database and prints an
// ASCII summary table of registered projects and currently running or queued jobs.
func TestCLI_Status_CLI4(t *testing.T) {
	// Create an isolated temporary directory for test config and SQLite database
	tempDir := t.TempDir()

	// Initialize config inside temp directory (creates config.yaml)
	cfg, err := config.Load(tempDir)
	if err != nil {
		t.Fatalf("config.Load failed: %v", err)
	}

	// Seed database with mock project and active job
	dbPath := filepath.Join(cfg.DataDir, "garagefab.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open failed: %v", err)
	}

	// Create test project record
	p := &store.Project{Name: "my-service", RepoPath: "/tmp/my-service"}
	if err := db.Projects().CreateProject(context.Background(), p); err != nil {
		t.Fatalf("CreateProject failed: %v", err)
	}

	// Create test job record in Coding stage
	j := &store.Job{
		ProjectID: p.ID,
		WorkType:  store.WorkTypeRefactor,
		Title:     "Refactor cache layer",
		Stage:     store.StageCoding,
		Status:    store.StatusRunning,
	}
	if err := db.Jobs().CreateJob(context.Background(), j); err != nil {
		t.Fatalf("CreateJob failed: %v", err)
	}
	// Close database connection before CLI command reopens it
	db.Close()

	// Set CLI flag to target our temporary data directory
	dataDirFlag = tempDir
	defer func() { dataDirFlag = "" }() // Reset flag after test completes

	// Capture CLI standard output in an in-memory byte buffer
	buf := new(bytes.Buffer)
	statusCmd.SetOut(buf)

	// Execute Cobra status command directly
	err = statusCmd.RunE(statusCmd, []string{})
	if err != nil {
		t.Fatalf("statusCmd.RunE failed: %v", err)
	}

	// Verify required columns and values are present in printed output (CLI-4)
	out := buf.String()
	if !strings.Contains(out, "my-service") ||
		!strings.Contains(out, "04_Coding") ||
		!strings.Contains(out, "running") ||
		!strings.Contains(out, "Refactor cache layer") {
		t.Errorf("CLI-4 violated: expected status table to contain job details, got:\n%s", out)
	}
}
