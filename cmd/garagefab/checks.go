// Package main is the entry point for the garagefab binary.
//
// checks.go contains pre-flight startup validations (spec CLI-7).
// Before launching the server, database, or scheduler, the application performs
// sanity checks on the host environment (verifying Git installation, version 2.30+, and port availability).
package main

import (
	"fmt"
	"net"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// gitVersionRegex extracts the major, minor, and patch numbers from "git version X.Y.Z" output.
// In Go, regexp.MustCompile compiles the regex at package initialization time.
var gitVersionRegex = regexp.MustCompile(`git version (\d+)\.(\d+)(?:\.(\d+))?`)

// validateGitVersionOutput parses the string output of `git --version` and verifies
// that the installed Git version satisfies the minimum requirement (Git 2.30+ per spec OQ-13).
//
// In Go, functions return multiple values (result, error) instead of throwing exceptions.
func validateGitVersionOutput(output string) error {
	// FindStringSubmatch returns captured regex groups: [full_match, major, minor, patch]
	matches := gitVersionRegex.FindStringSubmatch(output)
	if len(matches) < 3 {
		return fmt.Errorf("cannot parse git version from output: %q", strings.TrimSpace(output))
	}

	// strconv.Atoi converts string to integer (equivalent to Integer.parseInt() in Java).
	major, err := strconv.Atoi(matches[1])
	if err != nil {
		// %w wraps the inner error, preserving the error chain (like new Exception("...", cause) in Java).
		return fmt.Errorf("invalid git major version: %w", err)
	}

	minor, err := strconv.Atoi(matches[2])
	if err != nil {
		return fmt.Errorf("invalid git minor version: %w", err)
	}

	// Rule: Garagefab requires Git 2.30+ for modern git-worktree lifecycle support.
	if major < 2 || (major == 2 && minor < 30) {
		return fmt.Errorf("git version 2.30+ is required, found %d.%d", major, minor)
	}

	return nil // nil represents the absence of error (successful execution).
}

// checkGitAvailable verifies that Git is installed in the system PATH and is executable.
// In Go, os/exec.Command runs a subprocess (equivalent to Java's ProcessBuilder).
func checkGitAvailable() error {
	// Execute 'git --version' and capture the stdout bytes.
	out, err := exec.Command("git", "--version").Output()
	if err != nil {
		return fmt.Errorf("startup check: git is not installed or not in PATH: %w", err)
	}

	// Validate the extracted version text.
	if err := validateGitVersionOutput(string(out)); err != nil {
		return fmt.Errorf("startup check: %w", err)
	}

	return nil
}

// checkPortAvailable ensures the target listen address (e.g., "127.0.0.1:7878") is not
// already bound by another process. It prevents unhelpful panics during server boot.
func checkPortAvailable(addr string) error {
	// Attempt to bind a temporary TCP listener to the address.
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("startup check: port on %s is not available (already in use): %w", addr, err)
	}

	// Immediately close the temporary listener so Garagefab can bind it during server startup.
	_ = ln.Close()
	return nil
}
