package store_test

import (
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/garagefab/garagefab/internal/store"
)

func TestOpen_FreshDatabase_AppliesMigrations(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "garagefab.db")

	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer db.Close()

	var version string
	err = db.QueryRow("SELECT value FROM _meta WHERE key = 'schema_version'").Scan(&version)
	if err != nil {
		t.Fatalf("failed to query _meta: %v", err)
	}

	if version != "1" {
		t.Errorf("expected schema_version '1', got %q", version)
	}
}

func TestParallelReader_NFR7(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "parallel.db")

	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer db.Close()

	// Ensure auxiliary table for writes so _meta stays version 1
	_, err = db.Exec("CREATE TABLE IF NOT EXISTS test_parallel (id INTEGER PRIMARY KEY, val TEXT)")
	if err != nil {
		t.Fatalf("create test table failed: %v", err)
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 200)

	// Writer goroutine
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

	// Multiple Reader goroutines
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

	wg.Wait()
	close(errCh)

	for err := range errCh {
		if strings.Contains(err.Error(), "database is locked") {
			t.Fatalf("encountered lock error during parallel access (NFR-7): %v", err)
		} else {
			t.Fatalf("unexpected database error: %v", err)
		}
	}
}

func TestSchemaVersion_TooNew_CLI7(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "version.db")

	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("initial Open failed: %v", err)
	}

	_, err = db.Exec("UPDATE _meta SET value = '99' WHERE key = 'schema_version'")
	if err != nil {
		t.Fatalf("update schema_version failed: %v", err)
	}
	db.Close()

	// Attempt to open with too-new schema version
	_, err = store.Open(dbPath)
	if err == nil {
		t.Fatal("expected error when schema is newer than binary, got nil")
	}

	if !strings.Contains(err.Error(), "newer than binary") && !strings.Contains(err.Error(), "version 99") {
		t.Errorf("expected error about newer schema version, got: %v", err)
	}
}
