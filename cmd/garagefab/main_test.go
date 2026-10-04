// Package main_test contains integration and end-to-end blackbox tests for the CLI.
//
// ==============================================================================
// GO TESTING CONCEPTS & ARCHITECTURAL PATTERNS:
//
//  1. Black-Box Testing (`package main_test` vs `package main`):
//     In Go, tests in the same directory can declare `package <name>_test`.
//     This forces the test code to import `<name>` as an external consumer, testing
//     only its public/exported interface. It prevents tests from relying on private internals.
//
//  2. `TestMain(m *testing.M)`:
//     `TestMain` is Go's suite-level setup and teardown hook.
//     In Java/JUnit 5, this is equivalent to `@BeforeAll` / `@AfterAll` at the suite level.
//     If `TestMain` is defined in a test file, `go test` calls `TestMain(m)` instead of
//     running tests directly. `m.Run()` executes the test cases and returns an exit code.
//
//  3. Subprocess Execution (`os/exec.Command`):
//     Integration testing of a CLI binary requires compiling the binary and launching it
//     as a real operating system process. This tests CLI argument parsing, OS signals,
//     process exit codes, file locks, and network bindings exactly as a user experiences them.
//
// ==============================================================================
package main_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMain acts as the global test runner for this package.
// It compiles the garagefab binary to `bin/garagefab` before running any CLI tests.
func TestMain(m *testing.M) {
	// Determine target binary output directory
	binDir := filepath.Join("..", "..", "bin")
	_ = os.MkdirAll(binDir, 0755)
	binPath := filepath.Join(binDir, "garagefab")

	// Compile binary using standard `go build`
	cmd := exec.Command("go", "build", "-o", binPath, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to build garagefab binary for tests: %v\noutput: %s\n", err, string(out))
		os.Exit(1)
	}

	// m.Run() executes all Test* functions in this package.
	// os.Exit passes the test outcome exit code back to the `go test` runner.
	os.Exit(m.Run())
}

// TestCLI_Version_CLI5 tests requirement CLI-5:
// Executing `garagefab version` outputs the application version string.
func TestCLI_Version_CLI5(t *testing.T) {
	cmd := exec.Command("../../bin/garagefab", "version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("version command failed: %v, output: %s", err, string(out))
	}
	if !strings.Contains(string(out), "garagefab version") {
		t.Errorf("expected version output, got: %s", string(out))
	}
}

// TestCLI_Start_And_SecondInstance_RCV5_CLI1 tests requirements:
// - CLI-1: `garagefab start` launches the server and opens default ports.
// - CLI-6: Creates config.yaml and garagefab.db if not present.
// - CLI-7: Refuses to start if the target HTTP port is already bound.
// - RCV-5: Refuses to start if another instance is already running on the same data directory.
func TestCLI_Start_And_SecondInstance_RCV5_CLI1(t *testing.T) {
	tempDir := t.TempDir()
	port := 17878 // High non-default port to avoid collisions with local dev

	// Launch first instance in background
	var cmd1Buf bytes.Buffer
	cmd1 := exec.Command("../../bin/garagefab", "start", "--data-dir", tempDir, "--port", fmt.Sprintf("%d", port), "--no-open")
	cmd1.Stdout = &cmd1Buf
	cmd1.Stderr = &cmd1Buf
	if err := cmd1.Start(); err != nil {
		t.Fatalf("failed to start first instance: %v", err)
	}

	// Defer cleanup: Send SIGINT (Ctrl+C) to gracefully stop the background process
	defer func() {
		_ = cmd1.Process.Signal(os.Interrupt)
		_ = cmd1.Wait()
		if t.Failed() {
			t.Logf("cmd1 output: %s", cmd1Buf.String())
		}
	}()

	// Polling Loop: Wait up to 5 seconds for HTTP health endpoint to report ready
	healthURL := fmt.Sprintf("http://127.0.0.1:%d/api/health", port)
	var healthOk bool
	var lastErr error
	for i := 0; i < 50; i++ {
		time.Sleep(100 * time.Millisecond)
		resp, err := http.Get(healthURL)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode == http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			var data map[string]string
			if err := json.Unmarshal(body, &data); err == nil && data["status"] == "ok" {
				healthOk = true
				break
			}
		} else {
			lastErr = fmt.Errorf("status code: %d", resp.StatusCode)
			resp.Body.Close()
		}
	}

	if !healthOk {
		t.Fatalf("health endpoint did not become ready in time: last error: %v", lastErr)
	}

	// Verify that embedded UI is served at root URL (/)
	rootResp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
	if err != nil {
		t.Fatalf("failed to GET root: %v", err)
	}
	defer rootResp.Body.Close()
	rootBody, _ := io.ReadAll(rootResp.Body)
	if !strings.Contains(string(rootBody), "Garagefab") {
		t.Errorf("expected embedded UI at root, got: %s", string(rootBody))
	}

	// Verify single-instance lock refusal (RCV-5):
	// A second instance using the same data directory MUST exit with an error.
	cmd2 := exec.Command("../../bin/garagefab", "start", "--data-dir", tempDir, "--port", fmt.Sprintf("%d", port+1), "--no-open")
	out2, err2 := cmd2.CombinedOutput()
	if err2 == nil {
		t.Fatal("expected second instance on same data directory to fail, but it succeeded")
	}
	if !strings.Contains(string(out2), "another garagefab instance is already running") && !strings.Contains(string(out2), "garagefab.lock") {
		t.Errorf("expected lock error message, got: %s", string(out2))
	}

	// Verify busy port check (CLI-7):
	// A second instance with a different data directory but the SAME port MUST fail.
	tempDir2 := t.TempDir()
	cmd3 := exec.Command("../../bin/garagefab", "start", "--data-dir", tempDir2, "--port", fmt.Sprintf("%d", port), "--no-open")
	out3, err3 := cmd3.CombinedOutput()
	if err3 == nil {
		t.Fatal("expected start on busy port to fail, but it succeeded")
	}
	if !strings.Contains(string(out3), "already in use") {
		t.Errorf("expected port in use error message, got: %s", string(out3))
	}

	// Verify config.yaml and garagefab.db were automatically provisioned (CLI-6)
	if _, err := os.Stat(filepath.Join(tempDir, "config.yaml")); err != nil {
		t.Errorf("expected config.yaml to be created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tempDir, "garagefab.db")); err != nil {
		t.Errorf("expected garagefab.db to be created: %v", err)
	}

	// Verify the --port override is persisted to config.yaml (CLI-1), so readers
	// such as `garagefab open` and the garagefab-work skill use the bound port.
	cfgData, err := os.ReadFile(filepath.Join(tempDir, "config.yaml"))
	if err != nil {
		t.Fatalf("read config.yaml: %v", err)
	}
	wantListen := fmt.Sprintf("listen: 127.0.0.1:%d", port)
	if !strings.Contains(string(cfgData), wantListen) {
		t.Errorf("expected config.yaml to persist %q, got:\n%s", wantListen, string(cfgData))
	}
}
