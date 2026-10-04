// Package factory implements domain models, validators, and pipeline orchestration.
//
// ==============================================================================
// ARCHITECTURAL ROLE & DOMAIN SERVICE:
// Chain of Evidence Compiler & Markdown Formatter (Clean Architecture Core, APR-1..4).
//
// Role:
// Compiles an immutable audit trail of verification evidence for human sign-off at the gate.
// Synthesizes results strictly from already-executed step runs, review reports, and diff metrics.
//
// Constraints:
// - APR-3: Assembled ONLY from stored artifacts and step records (zero subprocesses/agents).
// - APR-4: Summarized into .garagefab/jobs/<id>/evidence.md and committed to Git.
//
// ENTERPRISE & JAVA / SPRING COMPARISON:
// In Spring Boot: Similar to an aggregation service compiling DTOs for a management dashboard
// from repository queries without re-triggering business processes.
// In Go: Pure domain logic compiling state into structured JSON representations and Markdown
// text, fully isolated from I/O or HTTP layers.
//
// ==============================================================================
package factory

import (
	"fmt"
	"strings"
)

// DiffStat represents aggregated Git diff metrics for a job from merge base to HEAD.
type DiffStat struct {
	FilesChanged int `json:"files_changed"`
	Insertions   int `json:"insertions"`
	Deletions    int `json:"deletions"`
}

// ProbeEvidence summarizes the failing-probe verification for bug fixes (PRB-5).
type ProbeEvidence struct {
	Status  string `json:"status"` // "pass" | "fail" | "skipped"
	LogPath string `json:"log_path,omitempty"`
}

// EvidenceSummary represents the structured evidence presented at the human gate (APR-1).
type EvidenceSummary struct {
	JobID          int64             `json:"job_id"`
	HeadSHA        string            `json:"head_sha"`
	BuildStatus    string            `json:"build_status"`    // "pass" | "fail" | "skipped"
	TestStatus     string            `json:"test_status"`     // "pass" | "fail" | "skipped"
	LintStatus     string            `json:"lint_status"`     // "pass" | "fail" | "skipped"
	Probe          ProbeEvidence     `json:"probe"`           // bug_fix probe verification (PRB-5)
	ReviewDecision string            `json:"review_decision"` // "approve" | "request_changes"
	RiskScores     map[string]int    `json:"risk_scores"`
	WarningsCount  int               `json:"warnings_count"`
	FilesChanged   int               `json:"files_changed"`
	Insertions     int               `json:"insertions"`
	Deletions      int               `json:"deletions"`
	DrillDowns     map[string]string `json:"drill_downs"` // URLs or paths for drill-down inspection (APR-2)
}

// BuildEvidence compiles step runs, review critique, and diff metrics into EvidenceSummary
// and generates the content for evidence.md (APR-1..4).
func BuildEvidence(job *Job, steps []*StepRun, review *ReviewReport, diff *DiffStat) (*EvidenceSummary, string) {
	summary := &EvidenceSummary{
		JobID:          job.ID,
		HeadSHA:        job.HeadSHA,
		BuildStatus:    "skipped",
		TestStatus:     "skipped",
		LintStatus:     "skipped",
		Probe:          ProbeEvidence{Status: "skipped"},
		ReviewDecision: "unknown",
		RiskScores:     make(map[string]int),
		DrillDowns:     make(map[string]string),
	}

	// 1. Inspect step runs for command outcomes
	for _, s := range steps {
		// The probe command is tracked separately (PRB-5); do not fold it into build/test/lint.
		if s.Kind == StepKindCommand && s.Executor == "probe" {
			continue
		}
		// Identify command step status
		switch s.Stage {
		case StageCoding:
			if strings.Contains(s.LogPath, "build") || s.Kind == StepKindCommand {
				switch s.Status {
				case StepStatusSuccess:
					summary.BuildStatus = "pass"
				case StepStatusFail:
					summary.BuildStatus = "fail"
				}
			}
			// Look for test commands
			if strings.Contains(s.LogPath, "test") {
				switch s.Status {
				case StepStatusSuccess:
					summary.TestStatus = "pass"
				case StepStatusFail:
					summary.TestStatus = "fail"
				}
			}
			if strings.Contains(s.LogPath, "lint") {
				switch s.Status {
				case StepStatusSuccess:
					summary.LintStatus = "pass"
				case StepStatusFail:
					summary.LintStatus = "fail"
				}
			}
		}
	}

	// 1b. Probe verification (PRB-5): the latest probe command step run wins.
	var probeStep *StepRun
	for _, s := range steps {
		if s.Kind == StepKindCommand && s.Executor == "probe" {
			if probeStep == nil || s.ID > probeStep.ID {
				probeStep = s
			}
		}
	}
	if probeStep != nil {
		if probeStep.Status == StepStatusSuccess {
			summary.Probe.Status = "pass"
		} else {
			summary.Probe.Status = "fail"
		}
		summary.Probe.LogPath = probeStep.LogPath
	}

	// 2. Synthesize Review metrics
	if review != nil {
		summary.ReviewDecision = review.Decision
		summary.RiskScores["side_effect"] = review.Risk.SideEffect.Score
		summary.RiskScores["performance"] = review.Risk.Performance.Score
		summary.RiskScores["backward_compatibility"] = review.Risk.BackwardCompatibility.Score
		summary.WarningsCount = len(review.Warnings)
	}

	// 3. Diff statistics
	if diff != nil {
		summary.FilesChanged = diff.FilesChanged
		summary.Insertions = diff.Insertions
		summary.Deletions = diff.Deletions
	}

	// 4. Drill-down references (APR-2)
	summary.DrillDowns["diff"] = fmt.Sprintf("/api/jobs/%d/diff", job.ID)
	summary.DrillDowns["spec"] = fmt.Sprintf("/api/jobs/%d/artifacts/spec", job.ID)
	summary.DrillDowns["review"] = fmt.Sprintf("/api/jobs/%d/artifacts/review", job.ID)
	// The probe artifact only exists for bug fixes.
	if job.WorkType == WorkTypeBugFix {
		summary.DrillDowns["probe"] = fmt.Sprintf("/api/jobs/%d/artifacts/probe", job.ID)
	}
	summary.DrillDowns["evidence"] = fmt.Sprintf("/api/jobs/%d/artifacts/evidence", job.ID)

	// 5. Generate Markdown content for evidence.md (APR-4)
	var sb strings.Builder
	fmt.Fprintf(&sb, "# Evidence Summary for Job %d (%s)\n\n", job.ID, job.Title)
	fmt.Fprintf(&sb, "**Head SHA:** `%s`\n\n", summary.HeadSHA)

	sb.WriteString("## Command Results\n")
	fmt.Fprintf(&sb, "- **Build:** %s\n", summary.BuildStatus)
	fmt.Fprintf(&sb, "- **Tests:** %s\n", summary.TestStatus)
	fmt.Fprintf(&sb, "- **Lint:** %s\n", summary.LintStatus)
	if summary.BuildStatus == "skipped" && summary.TestStatus == "skipped" && summary.LintStatus == "skipped" {
		sb.WriteString("- _Verification commands were not run (none configured, or the docs profile runs only guardrails)._\n")
	}
	sb.WriteString("\n")

	if summary.Probe.Status != "skipped" {
		sb.WriteString("## Failing Probe\n")
		fmt.Fprintf(&sb, "- **Result:** %s\n", summary.Probe.Status)
		if summary.Probe.LogPath != "" {
			fmt.Fprintf(&sb, "- **Log:** `%s`\n", summary.Probe.LogPath)
		}
		sb.WriteString("\n")
	}

	sb.WriteString("## Independent Review\n")
	fmt.Fprintf(&sb, "- **Decision:** `%s`\n", summary.ReviewDecision)
	if review != nil {
		fmt.Fprintf(&sb, "- **Risk - Side Effect:** %d/5 (%s)\n", review.Risk.SideEffect.Score, review.Risk.SideEffect.Rationale)
		fmt.Fprintf(&sb, "- **Risk - Performance:** %d/5 (%s)\n", review.Risk.Performance.Score, review.Risk.Performance.Rationale)
		fmt.Fprintf(&sb, "- **Risk - Backward Compatibility:** %d/5 (%s)\n", review.Risk.BackwardCompatibility.Score, review.Risk.BackwardCompatibility.Rationale)
		fmt.Fprintf(&sb, "- **Pre-existing Warnings:** %d\n\n", summary.WarningsCount)
	}

	sb.WriteString("## Diff Metrics\n")
	fmt.Fprintf(&sb, "- **Files Changed:** %d\n", summary.FilesChanged)
	fmt.Fprintf(&sb, "- **Insertions (+):** %d\n", summary.Insertions)
	fmt.Fprintf(&sb, "- **Deletions (-):** %d\n\n", summary.Deletions)

	sb.WriteString("## Drill-Down Links\n")
	fmt.Fprintf(&sb, "- [Full Diff](%s)\n", summary.DrillDowns["diff"])
	fmt.Fprintf(&sb, "- [Job Spec](%s)\n", summary.DrillDowns["spec"])
	fmt.Fprintf(&sb, "- [Full Review Report](%s)\n", summary.DrillDowns["review"])
	if _, ok := summary.DrillDowns["probe"]; ok {
		fmt.Fprintf(&sb, "- [Probe Report](%s)\n", summary.DrillDowns["probe"])
	}

	return summary, sb.String()
}

// ParseDiffStat parses unified diff text into aggregate change metrics (FilesChanged, Insertions, Deletions).
//
// Go Idiom & Rationale:
// Instead of invoking shell commands or compiling regular expressions, we perform a single linear scan
// across lines using standard library `strings.HasPrefix`. This achieves zero-allocation metric collection
// while remaining fully portable and compatible with CGO_ENABLED=0.
//
// In enterprise systems, similar line-based diff metric extraction is commonly performed in CI/CD build
// plugins (e.g. Jenkins Git Plugin or SonarQube diff scanners) before running static analysis.
func ParseDiffStat(diff string) *DiffStat {
	stat := &DiffStat{}
	if strings.TrimSpace(diff) == "" {
		return stat
	}

	lines := strings.Split(diff, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "diff --git ") {
			stat.FilesChanged++
		} else if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			stat.Insertions++
		} else if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			stat.Deletions++
		}
	}
	return stat
}
