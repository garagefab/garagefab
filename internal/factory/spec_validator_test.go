// Package factory_test verifies the domain invariants of SpecValidator (SPC-4, spec §6.1).
//
// ==============================================================================
// TEST COVERAGE:
// - Valid feature spec: all headings, AC-1, ordered plan with AC ref.
// - Missing top-level # Title -> ErrSpecMissingTitle.
// - Missing required section -> ErrSpecMissingHeading.
// - Empty section body -> ErrSpecEmptySection.
// - Acceptance criteria without AC-<n> -> ErrSpecMissingAC.
// - Implementation plan without ordered items or AC ref -> ErrSpecMissingPlan.
// - Bug fix requires ## Reproduction.
// ==============================================================================
package factory_test

import (
	"errors"
	"testing"

	"github.com/garagefab/garagefab/internal/factory"
)

const validFeatureSpec = `# Add User Authentication

## Summary
Add JWT authentication endpoints for users.

## Goals and Non-Goals
Goals:
- Issue JWT upon valid login
- Verify token on protected routes
Non-Goals:
- OAuth2 providers

## Design
Expose /api/login accepting username and password.

## Acceptance Criteria
Given valid credentials When POST /api/login Then AC-1: return JWT token with 200.
Given invalid credentials When POST /api/login Then AC-2: return 401 Unauthorized.

## Implementation Plan
1. Create user store with password hashing (AC-1)
2. Add JWT token generation service (AC-1)
3. Add authentication middleware (AC-2)

## Test Plan
Unit tests for token service and integration tests for /api/login.

## Risks and Assumptions
Assumes secure token secret is configured in environment.
`

func TestValidateSpec_ValidFeature_SPC4(t *testing.T) {
	err := factory.ValidateSpec(validFeatureSpec, factory.WorkTypeFeature)
	if err != nil {
		t.Fatalf("expected valid spec to pass, got error: %v", err)
	}
}

func TestValidateSpec_MissingTitle_SPC4(t *testing.T) {
	specWithoutTitle := `## Summary
Some summary
## Goals and Non-Goals
Goals
## Design
Design
## Acceptance Criteria
AC-1 Given valid When test Then pass
## Implementation Plan
1. Do work for AC-1
## Test Plan
Test
## Risks and Assumptions
Risks
`
	err := factory.ValidateSpec(specWithoutTitle, factory.WorkTypeFeature)
	if !errors.Is(err, factory.ErrSpecMissingTitle) {
		t.Fatalf("expected ErrSpecMissingTitle, got: %v", err)
	}
}

func TestValidateSpec_MissingHeading_SPC4(t *testing.T) {
	// Missing "## Acceptance Criteria"
	specMissingAC := `# Feature Title
## Summary
Some summary
## Goals and Non-Goals
Goals
## Design
Design
## Implementation Plan
1. Do work for AC-1
## Test Plan
Test
## Risks and Assumptions
Risks
`
	err := factory.ValidateSpec(specMissingAC, factory.WorkTypeFeature)
	if !errors.Is(err, factory.ErrSpecMissingHeading) {
		t.Fatalf("expected ErrSpecMissingHeading, got: %v", err)
	}
}

func TestValidateSpec_EmptySection_SPC4(t *testing.T) {
	// "## Design" is completely empty
	specEmptyDesign := `# Feature Title
## Summary
Some summary
## Goals and Non-Goals
Goals
## Design

## Acceptance Criteria
AC-1 Given valid When test Then pass
## Implementation Plan
1. Do work for AC-1
## Test Plan
Test
## Risks and Assumptions
Risks
`
	err := factory.ValidateSpec(specEmptyDesign, factory.WorkTypeFeature)
	if !errors.Is(err, factory.ErrSpecEmptySection) {
		t.Fatalf("expected ErrSpecEmptySection, got: %v", err)
	}
}

func TestValidateSpec_MissingACIdentifier_SPC4(t *testing.T) {
	// Acceptance criteria has Given/When/Then but missing "AC-<n>"
	specNoACID := `# Feature Title
## Summary
Some summary
## Goals and Non-Goals
Goals
## Design
Design
## Acceptance Criteria
Given valid login when credentials submitted then grant access.
## Implementation Plan
1. Do work for AC-1
## Test Plan
Test
## Risks and Assumptions
Risks
`
	err := factory.ValidateSpec(specNoACID, factory.WorkTypeFeature)
	if !errors.Is(err, factory.ErrSpecMissingAC) {
		t.Fatalf("expected ErrSpecMissingAC, got: %v", err)
	}
}

func TestValidateSpec_MissingPlanOrderedOrACRef_SPC4(t *testing.T) {
	// Implementation plan has bullet points instead of ordered numbers, or no AC ref
	specBadPlan := `# Feature Title
## Summary
Some summary
## Goals and Non-Goals
Goals
## Design
Design
## Acceptance Criteria
AC-1 Given valid When test Then pass
## Implementation Plan
Just implement the code without numbering
## Test Plan
Test
## Risks and Assumptions
Risks
`
	err := factory.ValidateSpec(specBadPlan, factory.WorkTypeFeature)
	if !errors.Is(err, factory.ErrSpecMissingPlan) {
		t.Fatalf("expected ErrSpecMissingPlan, got: %v", err)
	}
}

func TestValidateSpec_BugFixReproduction_SPC4(t *testing.T) {
	// Bug fix requires ## Reproduction
	err := factory.ValidateSpec(validFeatureSpec, factory.WorkTypeBugFix)
	if !errors.Is(err, factory.ErrSpecMissingHeading) {
		t.Fatalf("expected ErrSpecMissingHeading for Reproduction in bug_fix, got: %v", err)
	}

	bugFixWithRepro := validFeatureSpec + "\n## Reproduction\nRun 'go test -run TestBug' which currently panics.\n"
	err = factory.ValidateSpec(bugFixWithRepro, factory.WorkTypeBugFix)
	if err != nil {
		t.Fatalf("expected bug_fix with Reproduction to pass, got: %v", err)
	}
}
