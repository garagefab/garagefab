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

func TestCLI_Start_And_SecondInstance_RCV5_CLI1(t *testing.T) {
	tempDir := t.TempDir()
	port := 17878 // Use a high random-ish port

	var cmd1Buf bytes.Buffer
	cmd1 := exec.Command("../../bin/garagefab", "start", "--data-dir", tempDir, "--port", fmt.Sprintf("%d", port), "--no-open")
	cmd1.Stdout = &cmd1Buf
	cmd1.Stderr = &cmd1Buf
	if err := cmd1.Start(); err != nil {
		t.Fatalf("failed to start first instance: %v", err)
	}
	defer func() {
		_ = cmd1.Process.Signal(os.Interrupt)
		_ = cmd1.Wait()
		if t.Failed() {
			t.Logf("cmd1 output: %s", cmd1Buf.String())
		}
	}()

	// Wait up to 5 seconds for health endpoint
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

	// Verify that placeholder UI is served at root
	rootResp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
	if err != nil {
		t.Fatalf("failed to GET root: %v", err)
	}
	defer rootResp.Body.Close()
	rootBody, _ := io.ReadAll(rootResp.Body)
	if !strings.Contains(string(rootBody), "Garagefab") {
		t.Errorf("expected embedded UI at root, got: %s", string(rootBody))
	}

	// Verify single-instance lock refusal (RCV-5)
	cmd2 := exec.Command("../../bin/garagefab", "start", "--data-dir", tempDir, "--port", fmt.Sprintf("%d", port+1), "--no-open")
	out2, err2 := cmd2.CombinedOutput()
	if err2 == nil {
		t.Fatal("expected second instance on same data directory to fail, but it succeeded")
	}
	if !strings.Contains(string(out2), "another garagefab instance is already running") && !strings.Contains(string(out2), "garagefab.lock") {
		t.Errorf("expected lock error message, got: %s", string(out2))
	}

	// Verify busy port check (CLI-7)
	tempDir2 := t.TempDir()
	cmd3 := exec.Command("../../bin/garagefab", "start", "--data-dir", tempDir2, "--port", fmt.Sprintf("%d", port), "--no-open")
	out3, err3 := cmd3.CombinedOutput()
	if err3 == nil {
		t.Fatal("expected start on busy port to fail, but it succeeded")
	}
	if !strings.Contains(string(out3), "already in use") {
		t.Errorf("expected port in use error message, got: %s", string(out3))
	}

	// Verify config.yaml and garagefab.db created (CLI-6)
	if _, err := os.Stat(filepath.Join(tempDir, "config.yaml")); err != nil {
		t.Errorf("expected config.yaml to be created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tempDir, "garagefab.db")); err != nil {
		t.Errorf("expected garagefab.db to be created: %v", err)
	}
}
