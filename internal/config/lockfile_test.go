// Package config_test contains unit tests for single-instance file locking.
//
// ==============================================================================
// GO TESTING CONCEPTS:
//
//  1. Testing Concurrency & Resource Contention:
//     This test verifies that OS file locking primitives (`flock`) correctly prevent
//     concurrent processes from corrupting shared data directories.
//
//  2. Resource Cleanup:
//     Always release acquired locks (`lock1.Release()`) and use `defer` to guarantee
//     release even if an intermediate assertion fails.
//
// ==============================================================================
package config_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/garagefab/garagefab/internal/config"
)

// TestLockFile_SecondInstance_RCV5 verifies requirement RCV-5:
// A second attempt to acquire the lock on an active data directory MUST fail,
// and after the first lock is released, subsequent acquisitions MUST succeed.
func TestLockFile_SecondInstance_RCV5(t *testing.T) {
	dataDir := t.TempDir()

	// 1. First instance acquires the exclusive file lock
	lock1, err := config.AcquireLock(dataDir)
	if err != nil {
		t.Fatalf("first AcquireLock failed: %v", err)
	}

	// 2. Second instance attempts to acquire the lock on the same directory — MUST FAIL
	lock2, err := config.AcquireLock(dataDir)
	if err == nil {
		if lock2 != nil {
			_ = lock2.Release()
		}
		t.Fatal("expected second AcquireLock to fail, but it succeeded")
	}

	// Verify error message indicates the lockfile path for clear user diagnostics
	expectedLockPath := filepath.Join(dataDir, "garagefab.lock")
	if !strings.Contains(err.Error(), expectedLockPath) && !strings.Contains(err.Error(), "garagefab.lock") {
		t.Errorf("expected error message to mention lock file path, got: %v", err)
	}

	// 3. Release the first lock
	if err := lock1.Release(); err != nil {
		t.Fatalf("first lock Release failed: %v", err)
	}

	// 4. Third instance should now succeed in acquiring the released lock
	lock3, err := config.AcquireLock(dataDir)
	if err != nil {
		t.Fatalf("AcquireLock after release failed: %v", err)
	}
	defer func() {
		if err := lock3.Release(); err != nil {
			t.Errorf("lock3.Release failed: %v", err)
		}
	}()
}
