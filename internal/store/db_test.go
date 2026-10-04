// Package store_test contains integration tests for the database connection and migrations.
//
// ==============================================================================
// GO TESTING CONCURRENCY & SQLITE WAL VERIFICATION:
//
//  1. Testing High-Concurrency SQLite (NFR-7):
//     In classic SQLite (journal_mode=DELETE), any write transaction locks the entire
//     database file, causing concurrent reader queries to fail immediately with "database is locked".
//     In WAL mode with our dual-pool design (1 writer connection, 10 reader connections):
//     - Readers read from WAL snapshots without waiting for writers.
//     - Writers write to the WAL log without waiting for readers.
//     `TestParallelReader_NFR7` stress-tests this by running concurrent writer and reader
//     goroutines and asserting zero lock errors.
//
// ==============================================================================
package store_test

import (
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/garagefab/garagefab/internal/store"
)

// TestOpen_FreshDatabase_AppliesMigrations verifies that initializing a fresh SQLite database
// automatically applies embedded goose migrations and sets the schema version.
func TestOpen_FreshDatabase_AppliesMigrations(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "garagefab.db")

	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer db.Close()

	// Query _meta table populated by migrations
	var version string
	err = db.QueryRow("SELECT value FROM _meta WHERE key = 'schema_version'").Scan(&version)
	if err != nil {
		t.Fatalf("failed to query _meta: %v", err)
	}

	if version != "3" {
		t.Errorf("expected schema_version '3', got %q", version)
	}
}

// TestParallelReader_NFR7 validates requirement NFR-7:
// Readers never block writers and writers never block readers under WAL mode.
func TestParallelReader_NFR7(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "parallel.db")

	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer db.Close()

	// Create a test table for high-frequency writes
	_, err = db.Exec("CREATE TABLE IF NOT EXISTS test_parallel (id INTEGER PRIMARY KEY, val TEXT)")
	if err != nil {
		t.Fatalf("create test table failed: %v", err)
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 200)

	// Writer goroutine: executes 50 rapid INSERT queries
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			if _, err := db.Exec("INSERT INTO test_parallel (val) VALUES (?)", "write-test"); err != nil {
				errCh <- err
				return
			}
		}
	}()

	// 3 Reader goroutines: execute 50 rapid SELECT COUNT(*) queries concurrently with writes
	for r := 0; r < 3; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				var count int
				if err := db.QueryRow("SELECT COUNT(*) FROM test_parallel").Scan(&count); err != nil {
					errCh <- err
					return
				}
			}
		}()
	}

	// Wait for all reader and writer goroutines to complete
	wg.Wait()
	close(errCh)

	// Verify that no goroutine encountered a "database is locked" error
	for err := range errCh {
		if strings.Contains(err.Error(), "database is locked") {
			t.Fatalf("encountered lock error during parallel access (NFR-7): %v", err)
		} else {
			t.Fatalf("unexpected database error: %v", err)
		}
	}
}

// TestSchemaVersion_TooNew_CLI7 verifies requirement CLI-7:
// Starting on a database with a schema newer than this binary knows how to handle
// is refused to prevent silent data corruption.
func TestSchemaVersion_TooNew_CLI7(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "version.db")

	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("initial Open failed: %v", err)
	}

	// Artificially increment schema version to 99 in _meta table
	_, err = db.Exec("UPDATE _meta SET value = '99' WHERE key = 'schema_version'")
	if err != nil {
		t.Fatalf("update schema_version failed: %v", err)
	}
	db.Close()

	// Attempt to re-open the database with simulated future schema
	_, err = store.Open(dbPath)
	if err == nil {
		t.Fatal("expected error when schema is newer than binary, got nil")
	}

	if !strings.Contains(err.Error(), "newer than binary") && !strings.Contains(err.Error(), "version 99") {
		t.Errorf("expected error about newer schema version, got: %v", err)
	}
}
