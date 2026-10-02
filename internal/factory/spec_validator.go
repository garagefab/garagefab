// Package factory implements domain models, validators, and pipeline orchestration.
//
// ==============================================================================
// ARCHITECTURAL ROLE & DOMAIN SPECIFICATION:
// Domain Service / Pure Domain Validator (Clean Architecture Core, SPC-4, spec §6.1).
//
// In Clean / Hexagonal Architecture:
// `SpecValidator` represents a pure Domain Service. It contains ZERO I/O, no network calls,
// no SQL queries, and zero external framework dependencies. It evaluates whether a Markdown
// specification document meets the structural and content invariants mandated by spec §6.1.
//
// ENTERPRISE & JAVA / SPRING COMPARISON:
// In Spring Boot: This is analogous to a JSR-303 custom Bean Validator or Domain Specification
// pattern (`org.springframework.validation.Validator`).
// In Go: We implement pure functions operating on primitive strings (`ValidateSpec(content, workType) error`),
// returning typed sentinel errors or detailed validation failure collections without requiring reflection.
//
// ==============================================================================
package factory

import (
	"bufio"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var (
	// ErrSpecMissingTitle indicates missing top-level '# Title' heading.
	ErrSpecMissingTitle = errors.New("spec: missing top-level # Title heading")
	// ErrSpecMissingHeading indicates one or more required '## Heading' sections are absent.
	ErrSpecMissingHeading = errors.New("spec: missing required second-level heading")
	// ErrSpecEmptySection indicates a required section has no body content.
	ErrSpecEmptySection = errors.New("spec: section body cannot be empty")
	// ErrSpecMissingAC indicates 'Acceptance Criteria' contains no AC-<n> identifier.
	ErrSpecMissingAC = errors.New("spec: Acceptance Criteria must contain at least one AC-<n> item")
	// ErrSpecMissingPlan indicates 'Implementation Plan' contains no ordered items referencing AC-<n>.
	ErrSpecMissingPlan = errors.New("spec: Implementation Plan must contain ordered items referencing AC-<n>")
)

// Required second-level headings per spec §6.1.
var requiredFeatureHeadings = []string{
	"Summary",
	"Goals and Non-Goals",
	"Design",
	"Acceptance Criteria",
	"Implementation Plan",
	"Test Plan",
	"Risks and Assumptions",
}

var (
	reH1      = regexp.MustCompile(`^#\s+(.+)$`)
	reH2      = regexp.MustCompile(`^##\s+(.+)$`)
	reACID    = regexp.MustCompile(`\bAC-\d+\b`)
	reOrdered = regexp.MustCompile(`^\s*\d+\.\s+(.+)$`)
)

// ValidateSpec verifies that a job specification adheres to the data contract in spec §6.1 (SPC-4).
//
// Rules enforced:
// 1. Top-level '# <Title>' is present and non-empty.
// 2. All required '##' headings are present and contain non-empty body text.
// 3. '## Acceptance Criteria' contains at least one 'AC-<n>' criterion.
// 4. '## Implementation Plan' contains at least one ordered item ('1. ...') referencing 'AC-<n>'.
// 5. If workType is 'bug_fix', '## Reproduction' is also required.
func ValidateSpec(content string, workType string) error {
	if strings.TrimSpace(content) == "" {
		return errors.New("spec: content cannot be empty")
	}

	scanner := bufio.NewScanner(strings.NewReader(content))
	hasTitle := false
	currentH2 := ""
	sections := make(map[string]*strings.Builder)

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		// Check for top-level title
		if m := reH1.FindStringSubmatch(trimmed); len(m) > 1 {
			if strings.TrimSpace(m[1]) != "" {
				hasTitle = true
			}
			continue
		}

		// Check for second-level heading
		if m := reH2.FindStringSubmatch(trimmed); len(m) > 1 {
			headingTitle := strings.TrimSpace(m[1])
			// Strip HTML comments (e.g. <!-- bug_fix only -->)
			if idx := strings.Index(headingTitle, "<!--"); idx != -1 {
				headingTitle = strings.TrimSpace(headingTitle[:idx])
			}
			currentH2 = headingTitle
			if sections[currentH2] == nil {
				sections[currentH2] = &strings.Builder{}
			}
			continue
		}

		// Accumulate body text into current H2 section
		if currentH2 != "" && trimmed != "" {
			sections[currentH2].WriteString(trimmed)
			sections[currentH2].WriteString("\n")
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("spec: scan error: %w", err)
	}

	if !hasTitle {
		return ErrSpecMissingTitle
	}

	// Determine required headings list
	required := make([]string, len(requiredFeatureHeadings))
	copy(required, requiredFeatureHeadings)
	if workType == WorkTypeBugFix {
		required = append(required, "Reproduction")
	}

	// Verify all required headings exist and are non-empty
	for _, reqHeading := range required {
		bodyBuilder, exists := findSectionCaseInsensitive(sections, reqHeading)
		if !exists {
			return fmt.Errorf("%w: '## %s'", ErrSpecMissingHeading, reqHeading)
		}
		if strings.TrimSpace(bodyBuilder.String()) == "" {
			return fmt.Errorf("%w: '## %s'", ErrSpecEmptySection, reqHeading)
		}
	}

	// Verify 'Acceptance Criteria' contains at least one AC-<n> identifier
	acBody, _ := findSectionCaseInsensitive(sections, "Acceptance Criteria")
	if !reACID.MatchString(acBody.String()) {
		return ErrSpecMissingAC
	}

	// Verify 'Implementation Plan' contains an ordered item referencing AC-<n>
	planBody, _ := findSectionCaseInsensitive(sections, "Implementation Plan")
	planText := planBody.String()
	hasOrderedItem := false
	hasPlanACRef := false

	planScanner := bufio.NewScanner(strings.NewReader(planText))
	for planScanner.Scan() {
		pLine := planScanner.Text()
		if reOrdered.MatchString(pLine) {
			hasOrderedItem = true
		}
		if reACID.MatchString(pLine) {
			hasPlanACRef = true
		}
	}

	if !hasOrderedItem || !hasPlanACRef {
		return ErrSpecMissingPlan
	}

	return nil
}

// findSectionCaseInsensitive matches section headings case-insensitively.
func findSectionCaseInsensitive(sections map[string]*strings.Builder, target string) (*strings.Builder, bool) {
	targetLower := strings.ToLower(strings.TrimSpace(target))
	for h, b := range sections {
		if strings.ToLower(strings.TrimSpace(h)) == targetLower {
			return b, true
		}
	}
	return nil, false
}
