// Package config provides file locking primitives to enforce single-instance execution.
//
// ==============================================================================
// ARCHITECTURAL ROLE & PATTERNS:
// Concurrency Control & Single-Instance Guarantee (RCV-5).
//
// To prevent race conditions, database corruption, or port conflicts across multiple
// instances of Garagefab targeting the same data directory, an OS-level advisory lock
// is acquired on startup (`garagefab.lock`).
//
// GO CONCEPTS & JAVA COMPARISON:
//
// 1. Advisory File Locking (`syscall.Flock` vs Java `FileLock`):
//
//   - In Java: You would use `FileChannel.open(path).tryLock()`, which throws an
//     `OverlappingFileLockException` if already held in the JVM or returns null if
//     held by another process.
//
//   - In Go: `syscall.Flock(fd, flags)` makes a direct POSIX `flock(2)` system call
//     on the underlying OS file descriptor.
//
//   - `syscall.LOCK_EX`: Exclusive lock (only one process can hold it).
//
//   - `syscall.LOCK_NB`: Non-blocking (fails immediately instead of waiting/blocking).
//
//     2. The Single-Method Interface Idiom (`Releaser`):
//     Go standard library favors tiny interfaces (e.g. `io.Reader`, `io.Closer`).
//     Naming convention: an interface with one method typically takes the method name
//     plus "-er" (Release -> Releaser).
//
// ==============================================================================
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// Releaser represents an acquired resource (such as a file lock) that can be released.
//
// Java Equivalent: Similar to `AutoCloseable` or `Closeable`.
type Releaser interface {
	Release() error
}

// fileLock encapsulates an active OS file handle holding an exclusive flock.
//
// Go Concept: Private (unexported) struct.
// Because `fileLock` starts with a lowercase letter, external packages cannot
// construct it directly. They receive the public `Releaser` interface instead.
type fileLock struct {
	file *os.File
	path string
}

// AcquireLock attempts to obtain an exclusive, non-blocking lock on `<dataDir>/garagefab.lock`.
// If another instance already holds the lock, an error mentioning the lock path is returned.
//
// Requirement: RCV-5 (Single-instance daemon guardrail).
func AcquireLock(dataDir string) (Releaser, error) {
	// Ensure parent directory exists before attempting to open the lockfile
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return nil, fmt.Errorf("lockfile: create directory: %w", err)
	}

	lockPath := filepath.Join(dataDir, "garagefab.lock")
	// Open with O_CREATE | O_RDWR so the file is created if missing
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("lockfile: open %s: %w", lockPath, err)
	}

	// Try to acquire non-blocking exclusive flock via kernel syscall.
	// int(file.Fd()) extracts the native integer file descriptor (e.g., 3, 4, 5).
	err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		// Another process has the lock: close our file handle immediately
		_ = file.Close()
		return nil, fmt.Errorf("lockfile: cannot acquire lock on %s: another garagefab instance is already running", lockPath)
	}

	return &fileLock{
		file: file,
		path: lockPath,
	}, nil
}

// Release unlocks and closes the lock file.
//
// Go Concept: Pointer receiver method `(l *fileLock)`.
// By using a pointer receiver, changes to `l.file` modify the actual instance,
// allowing us to set `l.file = nil` to ensure idempotent releases.
func (l *fileLock) Release() error {
	if l.file == nil {
		return nil // Already released
	}
	// Unlock advisory flock
	_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	err := l.file.Close()
	l.file = nil
	return err
}
