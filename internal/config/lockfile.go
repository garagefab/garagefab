package config

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// Releaser represents an acquired resource that can be released.
type Releaser interface {
	Release() error
}

type fileLock struct {
	file *os.File
	path string
}

// AcquireLock attempts to obtain an exclusive non-blocking lock on <dataDir>/garagefab.lock.
// If another instance holds the lock, an error mentioning the lock path is returned.
func AcquireLock(dataDir string) (Releaser, error) {
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return nil, fmt.Errorf("lockfile: create directory: %w", err)
	}

	lockPath := filepath.Join(dataDir, "garagefab.lock")
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("lockfile: open %s: %w", lockPath, err)
	}

	// Try to acquire non-blocking exclusive flock
	err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("lockfile: cannot acquire lock on %s: another garagefab instance is already running", lockPath)
	}

	return &fileLock{
		file: file,
		path: lockPath,
	}, nil
}

// Release unlocks and closes the lock file.
func (l *fileLock) Release() error {
	if l.file == nil {
		return nil
	}
	_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	err := l.file.Close()
	l.file = nil
	return err
}
