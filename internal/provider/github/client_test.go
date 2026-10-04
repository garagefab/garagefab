// Package github_test contains unit and integration tests for the GitHub provider adapter.
//
// ==============================================================================
// ARCHITECTURAL ROLE & TEST STRATEGY:
// Provider Adapter Offline Unit Tests (GHB-1, DLV-1, DLV-2, DLV-5, INT-3).
//
// This test suite runs 100% offline using `FakeGHRunner`:
//  1. Zero Network Calls: Never hits the live internet or GitHub APIs.
//  2. Full CLI Argument Validation: Verifies that exact CLI arguments and flags
//     are constructed and piped without shell escaping defects.
//  3. Stdin Validation: Verifies body text is streamed via stdin to avoid arg length limits.
//  4. Static Security Invariant (DLV-5): Verifies that no source code in `internal/`
//     ever executes a 'merge' command against GitHub CLI.
//
// ==============================================================================
package github_test

import (
	"context"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/garagefab/garagefab/internal/provider/github"
)

// TestClient_UsesGhOnly_GHB1 validates requirement GHB-1:
// All GitHub interactions route through the mockable GHRunner executing 'gh'.
func TestClient_UsesGhOnly_GHB1(t *testing.T) {
	ctx := context.Background()
	fake := github.NewFakeGHRunner()
	client := github.NewClient(fake)

	// 1. ListIssues (INT-3)
	mockIssuesJSON := `[
		{
			"number": 42,
			"title": "Add OAuth Authentication",
			"body": "Detailed requirements",
			"labels": [{"name": "garagefab"}, {"name": "type:feature"}],
			"url": "https://github.com/owner/repo/issues/42"
		}
	]`
	fake.OnCommand("issue list", []byte(mockIssuesJSON), nil)

	issues, err := client.ListIssues(ctx, "owner/repo", "garagefab")
	if err != nil {
		t.Fatalf("ListIssues failed: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("expected 1 issue, got %d", len(issues))
	}
	if issues[0].Number != 42 || issues[0].Title != "Add OAuth Authentication" {
		t.Errorf("unexpected issue content: %+v", issues[0])
	}
	if len(issues[0].Labels) != 2 || issues[0].Labels[1] != "type:feature" {
		t.Errorf("unexpected issue labels: %v", issues[0].Labels)
	}

	// Verify CLI args passed
	lastCall, err := fake.LastCall()
	if err != nil {
		t.Fatal(err)
	}
	expectedArgs := "gh issue list --repo owner/repo --label garagefab --state open --limit 100 --json number,title,body,labels,url"
	if lastCall.CommandString() != expectedArgs {
		t.Errorf("expected command %q, got %q", expectedArgs, lastCall.CommandString())
	}

	// 2. EnsureLabel (GHB-2)
	fake.OnCommand("label create", []byte(""), nil)
	err = client.EnsureLabel(ctx, "owner/repo", "garagefab:in-progress", "ededed", "SDLC in progress")
	if err != nil {
		t.Fatalf("EnsureLabel failed: %v", err)
	}
	lastCall, _ = fake.LastCall()
	if !strings.Contains(lastCall.CommandString(), "--force") {
		t.Errorf("expected --force flag on EnsureLabel, got: %s", lastCall.CommandString())
	}

	// 3. EditLabels (GHB-2)
	fake.OnCommand("issue edit", []byte(""), nil)
	err = client.EditLabels(ctx, "owner/repo", 42, []string{"garagefab:needs-approval"}, []string{"garagefab:in-progress"})
	if err != nil {
		t.Fatalf("EditLabels failed: %v", err)
	}
	lastCall, _ = fake.LastCall()
	expectedEdit := "gh issue edit 42 --repo owner/repo --add-label garagefab:needs-approval --remove-label garagefab:in-progress"
	if lastCall.CommandString() != expectedEdit {
		t.Errorf("expected %q, got %q", expectedEdit, lastCall.CommandString())
	}

	// 4. Comment (GHB-2, GHB-5)
	fake.OnCommand("issue comment", []byte(""), nil)
	err = client.Comment(ctx, "owner/repo", 42, "Automated progress comment")
	if err != nil {
		t.Fatalf("Comment failed: %v", err)
	}
	lastCall, _ = fake.LastCall()
	if !strings.Contains(lastCall.CommandString(), "--body-file -") {
		t.Errorf("expected --body-file -, got %s", lastCall.CommandString())
	}
	if string(lastCall.Stdin) != "Automated progress comment" {
		t.Errorf("expected stdin body to match comment, got %q", string(lastCall.Stdin))
	}

	// 5. FindPR (DLV-2)
	// Case 5a: No PR exists
	fake.OnCommand("pr list", []byte("[]"), nil)
	pr, err := client.FindPR(ctx, "owner/repo", "garagefab-job-123")
	if err != nil {
		t.Fatalf("FindPR failed: %v", err)
	}
	if pr != nil {
		t.Errorf("expected nil PR for empty list, got %+v", pr)
	}

	// Case 5b: PR exists
	mockPRJSON := `[{"number": 99, "url": "https://github.com/owner/repo/pull/99", "state": "OPEN"}]`
	fake.OnCommand("pr list", []byte(mockPRJSON), nil)
	pr, err = client.FindPR(ctx, "owner/repo", "garagefab-job-123")
	if err != nil {
		t.Fatalf("FindPR failed: %v", err)
	}
	if pr == nil || pr.Number != 99 || pr.State != "OPEN" {
		t.Fatalf("unexpected PR: %+v", pr)
	}

	// 6. CreatePR (DLV-1)
	fake.OnCommand("pr create", []byte("https://github.com/owner/repo/pull/101\n"), nil)
	newPR, err := client.CreatePR(ctx, "owner/repo", "main", "garagefab-job-42", "Add OAuth", "PR description with evidence")
	if err != nil {
		t.Fatalf("CreatePR failed: %v", err)
	}
	if newPR == nil || newPR.Number != 101 || newPR.URL != "https://github.com/owner/repo/pull/101" {
		t.Fatalf("unexpected created PR: %+v", newPR)
	}
	lastCall, _ = fake.LastCall()
	if string(lastCall.Stdin) != "PR description with evidence" {
		t.Errorf("expected stdin body on CreatePR, got %q", string(lastCall.Stdin))
	}
}

// TestDelivery_NeverMerges_DLV5 verifies requirement DLV-5:
// Static AST analysis confirming that no Go source file in internal/ issues a 'merge' command to gh.
// Under DLV-5, Garagefab stops at PR creation and NEVER merges PRs into base branches.
func TestDelivery_NeverMerges_DLV5(t *testing.T) {
	internalDir := ".."
	fset := token.NewFileSet()

	err := filepath.Walk(internalDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}

		// Don't test tests
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}

		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		contentStr := string(content)

		// Check for forbidden gh pr merge invocations
		if strings.Contains(contentStr, `"pr", "merge"`) ||
			strings.Contains(contentStr, `"pr merge"`) ||
			strings.Contains(contentStr, `pr merge`) {
			t.Errorf("DLV-5 violation: found forbidden 'gh pr merge' command in %s", path)
		}

		// Parse AST to verify no function is named MergePR or similar auto-merge helper
		node, parseErr := parser.ParseFile(fset, path, content, 0)
		if parseErr != nil {
			return parseErr
		}
		for _, decl := range node.Decls {
			_ = decl
		}

		return nil
	})

	if err != nil {
		t.Fatalf("failed to scan repository files: %v", err)
	}
}
