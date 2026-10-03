//go:build realagent

package agent

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

// TestOpenCodeRunner_RealBinary verifies live execution against a locally installed opencode binary.
func TestOpenCodeRunner_RealBinary(t *testing.T) {
	if _, err := exec.LookPath("opencode"); err != nil {
		t.Skip("opencode binary not found on PATH; skipping real-binary test")
	}

	tmpDir := t.TempDir()
	runner := NewOpenCodeRunner("opencode", nil)

	req := AgentRequest{
		Role:         "coding",
		WorktreePath: tmpDir,
		Prompt:       "# Output verification\nPrint OK and finish immediately.",
		Timeout:      30 * time.Second,
	}

	res, err := runner.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("real opencode failed: %v", err)
	}

	if res.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d with summary: %s", res.ExitCode, res.Summary)
	}
}
