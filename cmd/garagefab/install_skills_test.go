// Package main provides CLI command definitions and verification tests.
//
// ==============================================================================
// ARCHITECTURAL ROLE & ENTERPRISE / JAVA SPRING COMPARISON:
// Driving Adapter Verification — Agent Skill Installation & Helper Security (CLI-3, HND-3..6).
//
// Tests verify:
//  1. Skill Discovery & Directory Creation: Detects agy and opencode, extracts embedded
//     skill assets with correct filesystem permissions (0644 files, 0755 scripts).
//  2. Dry-Run Isolation: Confirms `--dry-run` reports target paths without mutating the filesystem.
//  3. Helper Script Security (HND-5): Confirms `gf-api.sh` strictly restricts methods/paths,
//     refuses approve/reject attempts locally (Exit 2), and never leaks bearer tokens.
//  4. API Integration: Verifies `gf-api.sh` communicates with HTTP server using bearer auth.
//
// ==============================================================================
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestInstallSkills_BothAgents_HND3 tests requirement HND-3:
// When both agents are present, both receive the skill files with correct permissions,
// two "installed:" lines are printed, and re-running is idempotent.
func TestInstallSkills_BothAgents_HND3(t *testing.T) {
	tempHome := t.TempDir()

	// Simulate presence of both agents via config directories
	if err := os.MkdirAll(filepath.Join(tempHome, ".gemini"), 0755); err != nil {
		t.Fatalf("mkdir .gemini: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(tempHome, ".config", "opencode"), 0755); err != nil {
		t.Fatalf("mkdir opencode: %v", err)
	}

	var buf bytes.Buffer
	opts := InstallSkillsOptions{
		HomeDir: tempHome,
		DryRun:  false,
		Out:     &buf,
		LookPath: func(file string) (string, error) {
			return "", errors.New("not on path")
		},
	}

	if err := RunInstallSkills(opts); err != nil {
		t.Fatalf("RunInstallSkills error: %v", err)
	}

	out := buf.String()
	if strings.Count(out, "installed:") != 2 {
		t.Errorf("expected 2 'installed:' lines, got:\n%s", out)
	}

	agyTarget := filepath.Join(tempHome, ".gemini", "antigravity", "skills", "garagefab-work")
	opencodeTarget := filepath.Join(tempHome, ".config", "opencode", "skills", "garagefab-work")

	if !strings.Contains(out, agyTarget) {
		t.Errorf("output missing agy target %s", agyTarget)
	}
	if !strings.Contains(out, opencodeTarget) {
		t.Errorf("output missing opencode target %s", opencodeTarget)
	}

	// Verify installed files and permissions on disk
	for _, target := range []string{agyTarget, opencodeTarget} {
		skillFile := filepath.Join(target, "SKILL.md")
		info, err := os.Stat(skillFile)
		if err != nil {
			t.Fatalf("missing %s: %v", skillFile, err)
		}
		if info.Mode().Perm() != 0644 {
			t.Errorf("%s mode = %v, want 0644", skillFile, info.Mode().Perm())
		}

		scriptFile := filepath.Join(target, "scripts", "gf-api.sh")
		sInfo, err := os.Stat(scriptFile)
		if err != nil {
			t.Fatalf("missing %s: %v", scriptFile, err)
		}
		if sInfo.Mode().Perm() != 0755 {
			t.Errorf("%s mode = %v, want 0755", scriptFile, sInfo.Mode().Perm())
		}
	}

	// Verify idempotency: re-running succeeds with zero errors and preserves state
	buf.Reset()
	if err := RunInstallSkills(opts); err != nil {
		t.Fatalf("second RunInstallSkills run failed: %v", err)
	}
	if strings.Count(buf.String(), "installed:") != 2 {
		t.Errorf("expected 2 'installed:' lines on second run, got:\n%s", buf.String())
	}
}

// TestInstallSkills_OneAgentSkipped_HND3 tests requirement HND-3:
// When only agy is present, agy receives the skill and opencode is skipped with a note.
func TestInstallSkills_OneAgentSkipped_HND3(t *testing.T) {
	tempHome := t.TempDir()

	// Only simulate agy presence
	if err := os.MkdirAll(filepath.Join(tempHome, ".gemini"), 0755); err != nil {
		t.Fatalf("mkdir .gemini: %v", err)
	}

	var buf bytes.Buffer
	opts := InstallSkillsOptions{
		HomeDir: tempHome,
		DryRun:  false,
		Out:     &buf,
		LookPath: func(file string) (string, error) {
			return "", errors.New("not on path")
		},
	}

	if err := RunInstallSkills(opts); err != nil {
		t.Fatalf("RunInstallSkills failed: %v", err)
	}

	out := buf.String()
	if strings.Count(out, "installed:") != 1 {
		t.Errorf("expected 1 'installed:' line, got:\n%s", out)
	}
	if !strings.Contains(out, "skipped: opencode not found") {
		t.Errorf("expected skip note for opencode, got:\n%s", out)
	}
}

// TestInstallSkills_NeitherAgent_HND3 tests requirement HND-3:
// When neither agent is installed, prints two skipped lines and exits cleanly (exit 0).
func TestInstallSkills_NeitherAgent_HND3(t *testing.T) {
	tempHome := t.TempDir()

	var buf bytes.Buffer
	opts := InstallSkillsOptions{
		HomeDir: tempHome,
		DryRun:  false,
		Out:     &buf,
		LookPath: func(file string) (string, error) {
			return "", errors.New("not on path")
		},
	}

	if err := RunInstallSkills(opts); err != nil {
		t.Fatalf("RunInstallSkills should not return error when no agents found, got: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "skipped: agy not found") {
		t.Errorf("expected agy skip note, got:\n%s", out)
	}
	if !strings.Contains(out, "skipped: opencode not found") {
		t.Errorf("expected opencode skip note, got:\n%s", out)
	}
	if strings.Contains(out, "installed:") {
		t.Errorf("should not report installed when neither agent is found")
	}
}

// TestInstallSkills_DryRun_CLI3 tests requirement CLI-3:
// `install-skills --dry-run` prints targets and writes nothing to the filesystem.
func TestInstallSkills_DryRun_CLI3(t *testing.T) {
	tempHome := t.TempDir()

	// Simulate presence of agy and opencode
	if err := os.MkdirAll(filepath.Join(tempHome, ".gemini"), 0755); err != nil {
		t.Fatalf("mkdir .gemini: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(tempHome, ".config", "opencode"), 0755); err != nil {
		t.Fatalf("mkdir opencode: %v", err)
	}

	var buf bytes.Buffer
	opts := InstallSkillsOptions{
		HomeDir: tempHome,
		DryRun:  true,
		Out:     &buf,
		LookPath: func(file string) (string, error) {
			return "", errors.New("not on path")
		},
	}

	if err := RunInstallSkills(opts); err != nil {
		t.Fatalf("RunInstallSkills error: %v", err)
	}

	out := buf.String()
	if strings.Contains(out, "installed:") {
		t.Errorf("dry run should not report installed: %s", out)
	}
	if strings.Count(out, "target:") != 2 {
		t.Errorf("expected 2 'target:' lines in dry-run, got:\n%s", out)
	}

	// Verify no files were created under skills directories
	agySkillsDir := filepath.Join(tempHome, ".gemini", "antigravity", "skills")
	if _, err := os.Stat(agySkillsDir); !os.IsNotExist(err) {
		t.Errorf("dry-run should not create skills directory %s", agySkillsDir)
	}
	opencodeSkillsDir := filepath.Join(tempHome, ".config", "opencode", "skills")
	if _, err := os.Stat(opencodeSkillsDir); !os.IsNotExist(err) {
		t.Errorf("dry-run should not create skills directory %s", opencodeSkillsDir)
	}
}

// setupGFApiEnv creates a temporary test environment with a valid ~/.garagefab/config.yaml
// pointing to an HTTP test server.
func setupGFApiEnv(t *testing.T, serverURL, token string) string {
	t.Helper()
	tempHome := t.TempDir()
	gfHome := filepath.Join(tempHome, ".garagefab")
	if err := os.MkdirAll(gfHome, 0700); err != nil {
		t.Fatalf("mkdir gfHome: %v", err)
	}

	// Strip http:// prefix if present to test port/listen parsing
	listen := strings.TrimPrefix(serverURL, "http://")
	configContent := fmt.Sprintf("server:\n  listen: %s\n  api_token: %s\n", listen, token)
	if err := os.WriteFile(filepath.Join(gfHome, "config.yaml"), []byte(configContent), 0600); err != nil {
		t.Fatalf("write config.yaml: %v", err)
	}

	return tempHome
}

// TestGFApi_ClarificationAndSecrecy_HND4_HND5 verifies requirement HND-4 and HND-5:
// gf-api.sh communicates with HTTP server using Bearer authentication, handles GET and POST clarification,
// and strictly preserves token secrecy in stdout and stderr.
func TestGFApi_ClarificationAndSecrecy_HND4_HND5(t *testing.T) {
	testToken := "secret-bearer-token-1234567890abcdef"
	var receivedAuthHeader string
	var receivedAnswers []map[string]any

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuthHeader = r.Header.Get("Authorization")

		switch {
		case r.Method == "GET" && r.URL.Path == "/api/jobs/178":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":178,"stage":"02","status":"needs_clarification","worktree_path":"/tmp/wt-178"}`))
		case r.Method == "POST" && r.URL.Path == "/api/jobs/178/clarification":
			var payload struct {
				Answers []map[string]any `json:"answers"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			receivedAnswers = payload.Answers
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	tempHome := setupGFApiEnv(t, ts.URL, testToken)
	scriptPath, err := filepath.Abs("skills/garagefab-work/scripts/gf-api.sh")
	if err != nil {
		t.Fatalf("abs scriptPath: %v", err)
	}

	// 1. Test GET /api/jobs/178
	getCmd := exec.CommandContext(context.Background(), scriptPath, "GET", "/api/jobs/178")
	getCmd.Env = append(os.Environ(), "HOME="+tempHome, "GARAGEFAB_HOME="+filepath.Join(tempHome, ".garagefab"))
	var getOut, getErr bytes.Buffer
	getCmd.Stdout = &getOut
	getCmd.Stderr = &getErr

	if err := getCmd.Run(); err != nil {
		t.Fatalf("gf-api.sh GET failed: %v, stderr: %s", err, getErr.String())
	}

	if !strings.Contains(getOut.String(), `"needs_clarification"`) {
		t.Errorf("GET output missing expected JSON: %s", getOut.String())
	}
	if receivedAuthHeader != "Bearer "+testToken {
		t.Errorf("server received Authorization header %q, want %q", receivedAuthHeader, "Bearer "+testToken)
	}
	if strings.Contains(getOut.String(), testToken) || strings.Contains(getErr.String(), testToken) {
		t.Errorf("token leaked in GET output! out=%s, err=%s", getOut.String(), getErr.String())
	}

	// 2. Test POST /api/jobs/178/clarification
	postBody := `{"answers":[{"q":1,"answer":"Yes, use SQLite"}]}`
	postCmd := exec.CommandContext(context.Background(), scriptPath, "POST", "/api/jobs/178/clarification", postBody)
	postCmd.Env = append(os.Environ(), "HOME="+tempHome, "GARAGEFAB_HOME="+filepath.Join(tempHome, ".garagefab"))
	var postOut, postErr bytes.Buffer
	postCmd.Stdout = &postOut
	postCmd.Stderr = &postErr

	if err := postCmd.Run(); err != nil {
		t.Fatalf("gf-api.sh POST clarification failed: %v, stderr: %s", err, postErr.String())
	}

	if len(receivedAnswers) != 1 || receivedAnswers[0]["answer"] != "Yes, use SQLite" {
		t.Errorf("server received answers %v, want [{q:1, answer: 'Yes, use SQLite'}]", receivedAnswers)
	}
	if strings.Contains(postOut.String(), testToken) || strings.Contains(postErr.String(), testToken) {
		t.Errorf("token leaked in POST output! out=%s, err=%s", postOut.String(), postErr.String())
	}
}

// TestGFApi_DefenseInDepth_HND5 verifies requirement HND-5:
// gf-api.sh locally refuses any disallowed endpoint or method (e.g. approve, reject, delete) with Exit 2.
func TestGFApi_DefenseInDepth_HND5(t *testing.T) {
	scriptPath, err := filepath.Abs("skills/garagefab-work/scripts/gf-api.sh")
	if err != nil {
		t.Fatalf("abs scriptPath: %v", err)
	}

	tempHome := t.TempDir() // Config file not even needed because gate triggers before I/O!

	disallowedCases := []struct {
		name     string
		method   string
		endpoint string
		body     string
	}{
		{"approve attempt", "POST", "/api/jobs/178/approve", ""},
		{"reject attempt", "POST", "/api/jobs/178/reject", `{"reason":"no"}`},
		{"delete job", "DELETE", "/api/jobs/178", ""},
		{"get project", "GET", "/api/projects/1", ""},
		{"patch job", "PATCH", "/api/jobs/178", ""},
		{"cancel job via script", "POST", "/api/jobs/178/cancel", ""},
	}

	for _, tc := range disallowedCases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.CommandContext(context.Background(), scriptPath, tc.method, tc.endpoint, tc.body)
			cmd.Env = append(os.Environ(), "HOME="+tempHome)
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr

			runErr := cmd.Run()
			if runErr == nil {
				t.Fatalf("expected command to fail with exit 2, but succeeded")
			}

			var exitErr *exec.ExitError
			if !errors.As(runErr, &exitErr) {
				t.Fatalf("expected ExitError, got %v", runErr)
			}
			if exitErr.ExitCode() != 2 {
				t.Errorf("exit code = %d, want 2", exitErr.ExitCode())
			}

			if !strings.Contains(stderr.String(), "refused: not permitted by garagefab-work") {
				t.Errorf("stderr missing refusal notice, got: %s", stderr.String())
			}
		})
	}
}
