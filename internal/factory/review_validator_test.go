// Package factory_test verifies the domain invariants of ReviewValidator (REV-2, spec §6.2).
package factory_test

import (
	"errors"
	"testing"

	"github.com/garagefab/garagefab/internal/factory"
)

const validApproveReviewJSON = `{
  "schema_version": 1,
  "decision": "approve",
  "summary": "Implementation is clean and adheres to spec.",
  "risk": {
    "side_effect": {"score": 1, "rationale": "No unexpected side effects"},
    "performance": {"score": 2, "rationale": "Slight memory increase"},
    "backward_compatibility": {"score": 1, "rationale": "Full backward compatibility"}
  },
  "findings": [
    {"severity": "minor", "file": "internal/auth/jwt.go", "line": 42, "description": "Consider adding doc comment"}
  ],
  "warnings": [
    {"file": "internal/old/legacy.go", "description": "Pre-existing dead code"}
  ],
  "spec_coverage": [
    {"criterion": "AC-1", "status": "met", "note": "Verified by test"}
  ]
}`

func TestValidateReviewJSON_ValidApprove_REV2(t *testing.T) {
	report, err := factory.ValidateReviewJSON([]byte(validApproveReviewJSON))
	if err != nil {
		t.Fatalf("expected valid review to pass, got: %v", err)
	}
	if report.Decision != "approve" {
		t.Errorf("expected decision approve, got %s", report.Decision)
	}
	if len(report.Findings) != 1 {
		t.Errorf("expected 1 finding, got %d", len(report.Findings))
	}
	if len(report.Warnings) != 1 {
		t.Errorf("expected 1 warning, got %d", len(report.Warnings))
	}
}

func TestValidateReviewJSON_BlockingApproval_REV2(t *testing.T) {
	// Violation: decision is "approve", but has a "blocking" finding!
	jsonWithBlocking := `{
  "schema_version": 1,
  "decision": "approve",
  "summary": "Looks good.",
  "risk": {
    "side_effect": {"score": 1, "rationale": "ok"},
    "performance": {"score": 1, "rationale": "ok"},
    "backward_compatibility": {"score": 1, "rationale": "ok"}
  },
  "findings": [
    {"severity": "blocking", "file": "main.go", "line": 10, "description": "Security vulnerability"}
  ],
  "warnings": [],
  "spec_coverage": []
}`
	_, err := factory.ValidateReviewJSON([]byte(jsonWithBlocking))
	if !errors.Is(err, factory.ErrReviewBlockingApproval) {
		t.Fatalf("expected ErrReviewBlockingApproval, got: %v", err)
	}
}

func TestValidateReviewJSON_RequestChangesWithBlocking_REV2(t *testing.T) {
	// Valid: decision is "request_changes" with a "blocking" finding
	jsonRequestChanges := `{
  "schema_version": 1,
  "decision": "request_changes",
  "summary": "Must fix vulnerability.",
  "risk": {
    "side_effect": {"score": 4, "rationale": "High risk of leak"},
    "performance": {"score": 1, "rationale": "ok"},
    "backward_compatibility": {"score": 1, "rationale": "ok"}
  },
  "findings": [
    {"severity": "blocking", "file": "main.go", "line": 10, "description": "SQL injection"}
  ],
  "warnings": [],
  "spec_coverage": []
}`
	report, err := factory.ValidateReviewJSON([]byte(jsonRequestChanges))
	if err != nil {
		t.Fatalf("expected request_changes with blocking finding to pass, got: %v", err)
	}
	if report.Decision != "request_changes" {
		t.Errorf("expected decision request_changes, got %s", report.Decision)
	}
}

func TestValidateReviewJSON_InvalidScore_REV2(t *testing.T) {
	// Violation: score is 6 (outside 1..5)
	badScoreJSON := `{
  "schema_version": 1,
  "decision": "approve",
  "summary": "Summary",
  "risk": {
    "side_effect": {"score": 6, "rationale": "Out of range"},
    "performance": {"score": 1, "rationale": "ok"},
    "backward_compatibility": {"score": 1, "rationale": "ok"}
  },
  "findings": [],
  "warnings": [],
  "spec_coverage": []
}`
	_, err := factory.ValidateReviewJSON([]byte(badScoreJSON))
	if !errors.Is(err, factory.ErrReviewInvalidScore) {
		t.Fatalf("expected ErrReviewInvalidScore, got: %v", err)
	}
}

func TestValidateReviewJSON_InvalidVersion_REV2(t *testing.T) {
	badVersionJSON := `{
  "schema_version": 2,
  "decision": "approve",
  "summary": "Summary",
  "risk": {
    "side_effect": {"score": 1, "rationale": "ok"},
    "performance": {"score": 1, "rationale": "ok"},
    "backward_compatibility": {"score": 1, "rationale": "ok"}
  },
  "findings": [],
  "warnings": [],
  "spec_coverage": []
}`
	_, err := factory.ValidateReviewJSON([]byte(badVersionJSON))
	if !errors.Is(err, factory.ErrReviewInvalidVersion) {
		t.Fatalf("expected ErrReviewInvalidVersion, got: %v", err)
	}
}
