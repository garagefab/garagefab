// Package command verifies log-writer fault handling (spec §8: disk full).
package command

import (
	"context"
	"errors"
	"os"
	"testing"
)

// TestRunner_LogOpenFailure_ReturnsError verifies that a log-writer failure (disk full) is
// surfaced as an error instead of being swallowed, while the service keeps running (spec §8).
func TestRunner_LogOpenFailure_ReturnsError(t *testing.T) {
	orig := openLogFile
	t.Cleanup(func() { openLogFile = orig })
	openLogFile = func(name string, flag int, perm os.FileMode) (*os.File, error) {
		return nil, errors.New("no space left on device")
	}

	r := NewRunner()
	_, err := r.Run(context.Background(), RunOptions{
		WorkDir: t.TempDir(),
		Command: "echo hi",
		LogPath: "/tmp/garagefab-nfr-diskfull.log",
	})
	if err == nil {
		t.Fatal("expected an error when the log file cannot be opened")
	}
}
