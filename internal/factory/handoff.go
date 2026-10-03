// Package factory implements the core SDLC pipeline orchestrator and state engine.
//
// ==============================================================================
// ARCHITECTURAL ROLE & PATTERNS:
// Core Domain Service — Agent Handoff Command Formatter (HND-1, HND-2, HND-4).
//
// When a job pauses waiting for human intervention (clarification questions, spec sign-off,
// final gate approval, failure, or interruption), developers can resume work interactively
// inside their CLI agent (agy or opencode) via the `garagefab-work` skill.
//
// This file computes the copy-pasteable handoff shell command displayed in the web dashboard.
//
// JAVA / ENTERPRISE BACKEND COMPARISONS:
//
//  1. Pure Domain Helper:
//     Similar to a domain utility class in Spring (`HandoffCommandBuilder`), this logic
//     is strictly pure with zero external I/O or database dependencies.
//
//  2. Shell Escaping & Injection Protection:
//     When embedding filesystem paths into shell command strings, special characters
//     (especially spaces, apostrophes, and control chars) present shell injection risks.
//     We use standard POSIX single-quote escaping: enclosing the path in single quotes
//     and replacing embedded single quotes with `'\”`. Safe POSIX paths remain unquoted.
//
// ==============================================================================
package factory

import (
	"fmt"
	"strings"
)

// HandoffEligible reports whether a job status qualifies for developer handoff (HND-1).
// Handoff is enabled when a job is paused or stopped waiting for human action:
// - needs_clarification (02)
// - spec_review (02)
// - awaiting_approval (06)
// - failed (any stage)
// - interrupted (crash recovery / graceful shutdown)
func HandoffEligible(status string) bool {
	switch status {
	case StatusNeedsClarification, StatusSpecReview, StatusAwaitingApproval, StatusFailed, StatusInterrupted:
		return true
	default:
		return false
	}
}

// isSafePOSIXPathChar returns true if the rune requires no escaping in POSIX shell arguments.
func isSafePOSIXPathChar(r rune) bool {
	return (r >= 'a' && r <= 'z') ||
		(r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9') ||
		r == '_' || r == '@' || r == '%' || r == '+' ||
		r == '=' || r == ':' || r == ',' || r == '.' ||
		r == '/' || r == '-'
}

// quotePath applies POSIX single-quoting to file paths containing spaces or shell metacharacters.
func quotePath(path string) string {
	if path == "" {
		return "''"
	}
	needsQuote := false
	for _, r := range path {
		if !isSafePOSIXPathChar(r) {
			needsQuote = true
			break
		}
	}
	if !needsQuote {
		return path
	}

	// In POSIX shells, enclosing in single quotes preserves all characters literally.
	// To include a literal single quote: close quote ('), append escaped quote (\'), reopen quote (').
	escaped := strings.ReplaceAll(path, "'", `'\''`)
	return "'" + escaped + "'"
}

// HandoffCommand formats the exact CLI invocation string to resume a paused job (HND-1, HND-2).
// Example: `cd '/Users/me/my app' ; agy garagefab-work 178`
func HandoffCommand(projectPath, agent string, jobID int64) string {
	if projectPath == "" || agent == "" || jobID <= 0 {
		return ""
	}
	return fmt.Sprintf("cd %s ; %s garagefab-work %d", quotePath(projectPath), agent, jobID)
}
