package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/garagefab/garagefab/internal/config"
)

func TestLoadProjectConfig(t *testing.T) {
	tempDir := t.TempDir()

	// 1. When project.yaml does not exist, default should be returned
	cfg, err := config.LoadProjectConfig(tempDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.BaseRef != "origin/main" {
		t.Fatalf("expected default base_ref 'origin/main', got %s", cfg.BaseRef)
	}
	if len(cfg.Guardrails.ProtectedPaths) == 0 {
		t.Fatalf("expected default protected paths to include **/*_test.go")
	}

	// 2. When project.yaml exists, parse fields
	garagefabDir := filepath.Join(tempDir, ".garagefab")
	if err := os.MkdirAll(garagefabDir, 0755); err != nil {
		t.Fatal(err)
	}
	yamlContent := `
base_ref: main
commands:
  build: ["make build"]
  test: ["make test"]
  lint: ["make lint"]
guardrails:
  protected_paths: ["**/*_test.go", "vendor/**"]
  commands: ["git status"]
max_concurrent_jobs: 2
`
	if err := os.WriteFile(filepath.Join(garagefabDir, "project.yaml"), []byte(yamlContent), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err = config.LoadProjectConfig(tempDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.BaseRef != "main" {
		t.Fatalf("expected base_ref 'main', got %s", cfg.BaseRef)
	}
	if len(cfg.Commands.Build) != 1 || cfg.Commands.Build[0] != "make build" {
		t.Fatalf("unexpected build command: %v", cfg.Commands.Build)
	}
	if len(cfg.Commands.Test) != 1 || cfg.Commands.Test[0] != "make test" {
		t.Fatalf("unexpected test command: %v", cfg.Commands.Test)
	}
	if len(cfg.Guardrails.ProtectedPaths) != 2 {
		t.Fatalf("unexpected protected paths: %v", cfg.Guardrails.ProtectedPaths)
	}
	if cfg.MaxConcurrentJobs != 2 {
		t.Fatalf("expected max_concurrent_jobs 2, got %d", cfg.MaxConcurrentJobs)
	}
}

// TestLoadProjectConfig_TestPaths_GRD5 verifies that guardrails.test_paths is parsed and that
// it defaults to empty (no restriction) when omitted (GRD-5).
func TestLoadProjectConfig_TestPaths_GRD5(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Omitted test_paths -> empty (no restriction).
	cfg, err := config.LoadProjectConfig(tempDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Guardrails.TestPaths) != 0 {
		t.Errorf("expected empty default test_paths, got %v", cfg.Guardrails.TestPaths)
	}

	// 2. Explicit test_paths parsed.
	garagefabDir := filepath.Join(tempDir, ".garagefab")
	if err := os.MkdirAll(garagefabDir, 0755); err != nil {
		t.Fatal(err)
	}
	yamlContent := `
guardrails:
  protected_paths: ["**/*_test.go"]
  test_paths: ["**/*_test.go", "testdata/**"]
  commands: ["git status"]
`
	if err := os.WriteFile(filepath.Join(garagefabDir, "project.yaml"), []byte(yamlContent), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err = config.LoadProjectConfig(tempDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Guardrails.TestPaths) != 2 || cfg.Guardrails.TestPaths[1] != "testdata/**" {
		t.Errorf("unexpected test_paths: %v", cfg.Guardrails.TestPaths)
	}
}

func TestLoadProjectConfig_Agents_Validation(t *testing.T) {
	tempDir := t.TempDir()
	garagefabDir := filepath.Join(tempDir, ".garagefab")
	if err := os.MkdirAll(garagefabDir, 0755); err != nil {
		t.Fatal(err)
	}

	// 1. Valid agents with probe defaulting to coding, and timeouts.agent parsed
	validYAML := `
base_ref: main
agents:
  spec: agy
  coding: opencode
  review: agy
timeouts:
  agent: 45m
`
	projFile := filepath.Join(garagefabDir, "project.yaml")
	if err := os.WriteFile(projFile, []byte(validYAML), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.LoadProjectConfig(tempDir)
	if err != nil {
		t.Fatalf("unexpected error loading valid project.yaml: %v", err)
	}
	if cfg.Agents.Spec != "agy" {
		t.Errorf("expected spec 'agy', got %q", cfg.Agents.Spec)
	}
	if cfg.Agents.Coding != "opencode" {
		t.Errorf("expected coding 'opencode', got %q", cfg.Agents.Coding)
	}
	if cfg.Agents.Probe != "opencode" {
		t.Errorf("expected probe to default to coding 'opencode', got %q", cfg.Agents.Probe)
	}
	if cfg.Agents.Review != "agy" {
		t.Errorf("expected review 'agy', got %q", cfg.Agents.Review)
	}
	if cfg.Timeouts.Agent.Minutes() != 45 {
		t.Errorf("expected timeouts.agent to be 45m, got %v", cfg.Timeouts.Agent)
	}

	// 2. Explicit probe override is preserved
	explicitProbeYAML := `
agents:
  spec: agy
  probe: agy
  coding: opencode
  review: opencode
`
	if err := os.WriteFile(projFile, []byte(explicitProbeYAML), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err = config.LoadProjectConfig(tempDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Agents.Probe != "agy" {
		t.Errorf("expected probe 'agy', got %q", cfg.Agents.Probe)
	}

	// 3. Invalid agent name fails validation with key name
	invalidAgentYAML := `
agents:
  coding: foo
`
	if err := os.WriteFile(projFile, []byte(invalidAgentYAML), 0644); err != nil {
		t.Fatal(err)
	}
	_, err = config.LoadProjectConfig(tempDir)
	if err == nil {
		t.Fatal("expected error for unknown agent 'foo', got nil")
	}
	expectedErrMsg := `config: project.yaml: agents.coding: unknown agent "foo" (want agy|opencode)`
	if err.Error() != expectedErrMsg {
		t.Errorf("expected error %q, got %q", expectedErrMsg, err.Error())
	}
}

// TestProjectConfig_GitHub_PRJ4 validates requirement PRJ-4 and GHB-1:
// Parsing, default population, and semantic validation of project.yaml github settings.
func TestProjectConfig_GitHub_PRJ4(t *testing.T) {
	tempDir := t.TempDir()
	garagefabDir := filepath.Join(tempDir, ".garagefab")
	if err := os.MkdirAll(garagefabDir, 0755); err != nil {
		t.Fatal(err)
	}
	projFile := filepath.Join(garagefabDir, "project.yaml")

	// 1. Defaults when github section omitted
	cfg, err := config.LoadProjectConfig(tempDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.GitHub.Repo != "" {
		t.Errorf("expected empty default Repo, got %q", cfg.GitHub.Repo)
	}
	if cfg.GitHub.IntakeLabel != "garagefab" {
		t.Errorf("expected default IntakeLabel 'garagefab', got %q", cfg.GitHub.IntakeLabel)
	}
	if cfg.GitHub.PRIssueKeyword != "closes" {
		t.Errorf("expected default PRIssueKeyword 'closes', got %q", cfg.GitHub.PRIssueKeyword)
	}

	// 2. Valid custom GitHub configuration
	validYAML := `
github:
  repo: owner/my-repo
  intake_label: custom-intake
  pr_issue_keyword: refs
`
	if err := os.WriteFile(projFile, []byte(validYAML), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err = config.LoadProjectConfig(tempDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.GitHub.Repo != "owner/my-repo" {
		t.Errorf("expected Repo 'owner/my-repo', got %q", cfg.GitHub.Repo)
	}
	if cfg.GitHub.IntakeLabel != "custom-intake" {
		t.Errorf("expected IntakeLabel 'custom-intake', got %q", cfg.GitHub.IntakeLabel)
	}
	if cfg.GitHub.PRIssueKeyword != "refs" {
		t.Errorf("expected PRIssueKeyword 'refs', got %q", cfg.GitHub.PRIssueKeyword)
	}

	// 3. Invalid repo formats
	invalidRepos := []string{
		"invalid-repo-no-slash",
		"/missing-owner",
		"missing-repo/",
		"too/many/slashes/here",
	}
	for _, inv := range invalidRepos {
		yaml := "github:\n  repo: " + inv + "\n"
		if err := os.WriteFile(projFile, []byte(yaml), 0644); err != nil {
			t.Fatal(err)
		}
		_, err := config.LoadProjectConfig(tempDir)
		if err == nil {
			t.Errorf("expected error for invalid repo %q, got nil", inv)
		}
	}

	// 4. Invalid pr_issue_keyword
	invalidKeywordYAML := `
github:
  repo: owner/repo
  pr_issue_keyword: invalid
`
	if err := os.WriteFile(projFile, []byte(invalidKeywordYAML), 0644); err != nil {
		t.Fatal(err)
	}
	_, err = config.LoadProjectConfig(tempDir)
	if err == nil {
		t.Fatal("expected error for invalid pr_issue_keyword, got nil")
	}
}
