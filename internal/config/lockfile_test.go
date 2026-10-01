package config_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/garagefab/garagefab/internal/config"
)

func TestLockFile_SecondInstance_RCV5(t *testing.T) {
	dataDir := t.TempDir()

	// First instance acquires lock
	lock1, err := config.AcquireLock(dataDir)
	if err != nil {
		t.Fatalf("first AcquireLock failed: %v", err)
	}

	// Second instance must fail
	lock2, err := config.AcquireLock(dataDir)
	if err == nil {
		if lock2 != nil {
			_ = lock2.Release()
		}
		t.Fatal("expected second AcquireLock to fail, but it succeeded")
	}

	expectedLockPath := filepath.Join(dataDir, "garagefab.lock")
	if !strings.Contains(err.Error(), expectedLockPath) && !strings.Contains(err.Error(), "garagefab.lock") {
		t.Errorf("expected error message to mention lock file path, got: %v", err)
	}

	// Release first lock
	if err := lock1.Release(); err != nil {
		t.Fatalf("first lock Release failed: %v", err)
	}

	// Third instance should now succeed
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
