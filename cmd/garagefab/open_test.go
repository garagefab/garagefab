// Package main contains CLI command implementations and tests for garagefab.
//
// ==============================================================================
// ARCHITECTURAL ROLE & TESTING CONCEPTS:
// CLI Inbound Adapter Test (CLI-2).
//
// Tests that 'garagefab open --no-open' properly reads configuration,
// constructs the URL containing the token fragment, and prints it to stdout
// without crashing or attempting to open a browser window in CI.
//
// ==============================================================================
package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/garagefab/garagefab/internal/config"
)

// TestCLI_Open_PrintsURL_CLI2 verifies that "garagefab open --no-open"
// outputs the correct dashboard URL with token fragment (CLI-2).
func TestCLI_Open_PrintsURL_CLI2(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Create a valid config with known token and port
	cfg, err := config.Load(tempDir)
	if err != nil {
		t.Fatalf("config.Load failed: %v", err)
	}

	dataDirFlag = tempDir
	openNoOpenFlag = true
	portFlag = 8999

	defer func() {
		dataDirFlag = ""
		openNoOpenFlag = false
		portFlag = 0
	}()

	var buf bytes.Buffer
	openCmd.SetOut(&buf)
	openCmd.SetErr(&buf)

	if err := openCmd.RunE(openCmd, []string{}); err != nil {
		t.Fatalf("openCmd.RunE failed: %v", err)
	}

	out := buf.String()
	expectedURL := fmt.Sprintf("http://127.0.0.1:8999/login#token=%s", cfg.Server.APIToken)
	if !strings.Contains(out, expectedURL) {
		t.Errorf("expected output to contain %q, got: %s", expectedURL, out)
	}
}

// TestCLI_Open_DefaultPort_CLI2 verifies that default port is parsed from config.
func TestCLI_Open_DefaultPort_CLI2(t *testing.T) {
	tempDir := t.TempDir()

	cfg, err := config.Load(tempDir)
	if err != nil {
		t.Fatalf("config.Load failed: %v", err)
	}

	dataDirFlag = tempDir
	openNoOpenFlag = true
	portFlag = 0

	defer func() {
		dataDirFlag = ""
		openNoOpenFlag = false
		portFlag = 0
	}()

	var buf bytes.Buffer
	openCmd.SetOut(&buf)
	openCmd.SetErr(&buf)

	if err := openCmd.RunE(openCmd, []string{}); err != nil {
		t.Fatalf("openCmd.RunE failed: %v", err)
	}

	out := buf.String()
	expectedURL := fmt.Sprintf("http://127.0.0.1:7878/login#token=%s", cfg.Server.APIToken)
	if !strings.Contains(out, expectedURL) {
		t.Errorf("expected output to contain %q, got: %s", expectedURL, out)
	}
}

// TestCLI_Open_MissingConfig_ReturnsError verifies error handling when config fails.
func TestCLI_Open_MissingConfig_ReturnsError(t *testing.T) {
	// Point to an invalid file path as directory
	tmpFile := filepath.Join(t.TempDir(), "dummy.txt")
	_ = os.WriteFile(tmpFile, []byte("not-a-dir"), 0600)

	dataDirFlag = tmpFile
	openNoOpenFlag = true

	defer func() {
		dataDirFlag = ""
		openNoOpenFlag = false
	}()

	var buf bytes.Buffer
	openCmd.SetOut(&buf)
	openCmd.SetErr(&buf)

	err := openCmd.RunE(openCmd, []string{})
	if err == nil {
		t.Error("expected error loading config from non-directory, got nil")
	}
}
