// Package factory implements domain models, validators, and pipeline orchestration.
//
// ==============================================================================
// ARCHITECTURAL ROLE & DOMAIN SPECIFICATION:
// Domain Service / Pure Probe Report Validator (Clean Architecture Core, PRB-1, spec §6.3).
//
// Role:
// Validates the structured `probe.json` artifact produced by the failing-probe agent in
// stage `03_Failing_Probe`. It guarantees that the probe declares a runnable command and a
// non-empty list of safe, worktree-relative test files before the engine executes it.
//
// ENTERPRISE & JAVA / SPRING COMPARISON:
// In Spring Boot this is the equivalent of a Jackson DTO plus Bean Validation constraints
// (`@NotBlank`, `@Size(min = 1)`, `@Pattern`) applied to a request payload. In Go we deserialize
// into a strongly-typed struct and validate with plain functions — no reflection, no annotations.
//
// ==============================================================================
package factory

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

var (
	// ErrProbeBadJSON indicates invalid JSON syntax in probe.json.
	ErrProbeBadJSON = errors.New("probe: invalid JSON syntax")
	// ErrProbeInvalidVersion indicates schema_version is not 1.
	ErrProbeInvalidVersion = errors.New("probe: schema_version must be 1")
	// ErrProbeEmptyCommand indicates the probe command is empty.
	ErrProbeEmptyCommand = errors.New("probe: command cannot be empty")
	// ErrProbeEmptyFiles indicates the probe declared no test files.
	ErrProbeEmptyFiles = errors.New("probe: files cannot be empty")
	// ErrProbeInvalidPath indicates a probe file path is absolute or escapes the worktree.
	ErrProbeInvalidPath = errors.New("probe: files must be relative paths inside the worktree without \"..\"")
)

// ProbeReport represents the parsed and validated probe.json data contract (PRB-1, spec §6.3).
type ProbeReport struct {
	SchemaVersion int      `json:"schema_version"`
	Command       string   `json:"command"`
	Files         []string `json:"files"`
	Description   string   `json:"description"`
}

// ValidateProbeJSON parses and enforces the probe.json invariants (PRB-1, spec §6.3).
//
// Rules enforced:
//  1. Valid JSON with schema_version == 1.
//  2. command is non-empty.
//  3. files is non-empty; each entry is a non-empty, relative path inside the worktree
//     (not absolute and not containing a ".." segment).
func ValidateProbeJSON(data []byte) (*ProbeReport, error) {
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil, errors.New("probe: empty content")
	}

	var report ProbeReport
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrProbeBadJSON, err)
	}

	if report.SchemaVersion != 1 {
		return nil, fmt.Errorf("%w: got %d", ErrProbeInvalidVersion, report.SchemaVersion)
	}

	if strings.TrimSpace(report.Command) == "" {
		return nil, ErrProbeEmptyCommand
	}

	if len(report.Files) == 0 {
		return nil, ErrProbeEmptyFiles
	}
	for i, f := range report.Files {
		if !isSafeRelativePath(f) {
			return nil, fmt.Errorf("%w at index %d: got %q", ErrProbeInvalidPath, i, f)
		}
	}

	return &report, nil
}

// isSafeRelativePath reports whether p is a non-empty, relative path that stays inside the
// worktree (no absolute path, no ".." segment after cleaning).
func isSafeRelativePath(p string) bool {
	if strings.TrimSpace(p) == "" {
		return false
	}
	if filepath.IsAbs(p) {
		return false
	}
	clean := filepath.Clean(p)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}
