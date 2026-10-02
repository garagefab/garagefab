// Package factory_test verifies the domain invariants of BuildEvidence (APR-1..4).
package factory_test

import (
	"strings"
	"testing"

	"github.com/garagefab/garagefab/internal/factory"
)

func TestBuildEvidence_APR1_4(t *testing.T) {
	job := &factory.Job{
		ID:      42,
		Title:   "Implement OAuth2",
		HeadSHA: "commit-sha-42",
	}

	steps := []*factory.StepRun{
		{
			Stage:   factory.StageCoding,
			Kind:    factory.StepKindCommand,
			LogPath: "/logs/test.log",
			Status:  factory.StepStatusSuccess,
		},
	}

	review := &factory.ReviewReport{
		SchemaVersion: 1,
		Decision:      "approve",
		Summary:       "All good.",
		Risk: factory.ReviewRisk{
			SideEffect:            factory.ReviewRiskItem{Score: 1, Rationale: "low"},
			Performance:           factory.ReviewRiskItem{Score: 2, Rationale: "moderate"},
			BackwardCompatibility: factory.ReviewRiskItem{Score: 1, Rationale: "none"},
		},
		Warnings: []factory.ReviewWarning{
			{File: "legacy.go", Description: "old code"},
		},
	}

	diff := &factory.DiffStat{
		FilesChanged: 3,
		Insertions:   50,
		Deletions:    10,
	}

	summary, mdContent := factory.BuildEvidence(job, steps, review, diff)

	// 1. Verify summary struct fields (APR-1)
	if summary.JobID != 42 {
		t.Errorf("expected JobID = 42, got %d", summary.JobID)
	}
	if summary.HeadSHA != "commit-sha-42" {
		t.Errorf("expected HeadSHA = commit-sha-42, got %s", summary.HeadSHA)
	}
	if summary.ReviewDecision != "approve" {
		t.Errorf("expected ReviewDecision = approve, got %s", summary.ReviewDecision)
	}
	if summary.RiskScores["performance"] != 2 {
		t.Errorf("expected performance score 2, got %d", summary.RiskScores["performance"])
	}
	if summary.WarningsCount != 1 {
		t.Errorf("expected WarningsCount = 1, got %d", summary.WarningsCount)
	}
	if summary.FilesChanged != 3 || summary.Insertions != 50 || summary.Deletions != 10 {
		t.Errorf("diff metrics mismatch: %v", summary)
	}

	// 2. Verify drill-down references (APR-2)
	if summary.DrillDowns["diff"] != "/api/jobs/42/diff" {
		t.Errorf("expected drill down diff URL, got %s", summary.DrillDowns["diff"])
	}

	// 3. Verify markdown content (APR-4)
	if !strings.Contains(mdContent, "# Evidence Summary for Job 42") {
		t.Errorf("evidence markdown missing title, got:\n%s", mdContent)
	}
	if !strings.Contains(mdContent, "**Pre-existing Warnings:** 1") {
		t.Errorf("evidence markdown missing warnings count, got:\n%s", mdContent)
	}
	if !strings.Contains(mdContent, "**Files Changed:** 3") {
		t.Errorf("evidence markdown missing diff stats, got:\n%s", mdContent)
	}
}

func TestParseDiffStat(t *testing.T) {
	diff := `diff --git a/foo.go b/foo.go
--- a/foo.go
+++ b/foo.go
@@ -1,2 +1,4 @@
-old line
+new line 1
+new line 2
+new line 3
diff --git a/bar.go b/bar.go
--- a/bar.go
+++ b/bar.go
@@ -1 +0,0 @@
-removed line
`

	stat := factory.ParseDiffStat(diff)
	if stat.FilesChanged != 2 {
		t.Errorf("expected FilesChanged = 2, got %d", stat.FilesChanged)
	}
	if stat.Insertions != 3 {
		t.Errorf("expected Insertions = 3, got %d", stat.Insertions)
	}
	if stat.Deletions != 2 {
		t.Errorf("expected Deletions = 2, got %d", stat.Deletions)
	}

	emptyStat := factory.ParseDiffStat("")
	if emptyStat.FilesChanged != 0 || emptyStat.Insertions != 0 || emptyStat.Deletions != 0 {
		t.Errorf("expected zero stats for empty diff, got %+v", emptyStat)
	}
}
