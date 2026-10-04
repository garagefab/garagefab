// Package config manages configuration loading, validation, default provisioning,
// and single-instance process file locking for Garagefab.
//
// ==============================================================================
// ARCHITECTURAL ROLE & PATTERNS:
// Per-Project Configuration Loader (architecture.md §14).
//
// This file loads repository-level configuration from `<repo>/.garagefab/project.yaml`.
// It specifies:
// 1. Git base_ref (e.g. "origin/main" or "main").
// 2. Automated verification commands (build, test, lint) (COD-2, COD-3).
// 3. Protected path patterns and custom guardrail checks (GRD-1, GRD-3).
// 4. Optional per-project concurrency limits (SCH-2).
//
// ==============================================================================
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ProjectAgents defines the AI agent implementation assigned to each SDLC role (HND-2, architecture.md §14).
type ProjectAgents struct {
	Spec   string `yaml:"spec"`   // Agent for 02_Clarification_and_Spec (agy | opencode)
	Probe  string `yaml:"probe"`  // Agent for 03_Failing_Probe (defaults to coding if omitted)
	Coding string `yaml:"coding"` // Agent for 04_Coding
	Review string `yaml:"review"` // Agent for 05_Independent_Review
}

// ProjectTimeouts defines optional step timeout overrides per project.
type ProjectTimeouts struct {
	Agent time.Duration `yaml:"agent"` // Maximum runtime for agent steps in this project (COD-10)
}

// ProjectGitHub defines configuration for GitHub issue intake and delivery PR creation (GHB-1, PRJ-4).
type ProjectGitHub struct {
	Repo           string `yaml:"repo"`             // GitHub repository in "owner/repo" format (PRJ-4)
	IntakeLabel    string `yaml:"intake_label"`     // Label required to trigger intake (default: "garagefab")
	PRIssueKeyword string `yaml:"pr_issue_keyword"` // Issue closing keyword for PR body ("closes" | "refs", default: "closes")
}

// ProjectGuardrails defines protected path patterns, the probe test scope, and custom
// verification commands for a project (GRD-1, GRD-3, GRD-5).
type ProjectGuardrails struct {
	ProtectedPaths []string `yaml:"protected_paths"` // GRD-1: existing files agents must not modify
	TestPaths      []string `yaml:"test_paths"`      // GRD-5: probe step may only change these (empty = no restriction)
	Commands       []string `yaml:"commands"`        // GRD-3: custom verification commands
}

// ProjectYAML defines the schema for `<repo>/.garagefab/project.yaml`.
type ProjectYAML struct {
	BaseRef  string          `yaml:"base_ref"`
	GitHub   ProjectGitHub   `yaml:"github"`
	Agents   ProjectAgents   `yaml:"agents"`
	Timeouts ProjectTimeouts `yaml:"timeouts"`
	Commands struct {
		Build []string `yaml:"build"`
		Test  []string `yaml:"test"`
		Lint  []string `yaml:"lint"`
	} `yaml:"commands"`
	Guardrails        ProjectGuardrails `yaml:"guardrails"`
	MaxConcurrentJobs int               `yaml:"max_concurrent_jobs"`
}

// LoadProjectConfig reads and parses `<repo>/.garagefab/project.yaml`.
// If the file does not exist, it returns a default configuration with empty commands
// and standard test path protection (`**/*_test.go`).
func LoadProjectConfig(repoPath string) (*ProjectYAML, error) {
	cfg := &ProjectYAML{
		BaseRef: "origin/main",
		GitHub: ProjectGitHub{
			IntakeLabel:    "garagefab",
			PRIssueKeyword: "closes",
		},
	}
	cfg.Guardrails.ProtectedPaths = []string{"**/*_test.go"}

	configFile := filepath.Join(repoPath, ".garagefab", "project.yaml")
	data, err := os.ReadFile(configFile)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, fmt.Errorf("config: read project.yaml: %w", err)
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("config: unmarshal project.yaml: %w", err)
	}

	// Validate agent assignments and apply defaults (HND-2)
	validateAgent := func(role, name string) error {
		if name == "" {
			return nil
		}
		if name != "agy" && name != "opencode" {
			return fmt.Errorf("config: project.yaml: agents.%s: unknown agent %q (want agy|opencode)", role, name)
		}
		return nil
	}

	if err := validateAgent("spec", cfg.Agents.Spec); err != nil {
		return nil, err
	}
	if err := validateAgent("probe", cfg.Agents.Probe); err != nil {
		return nil, err
	}
	if err := validateAgent("coding", cfg.Agents.Coding); err != nil {
		return nil, err
	}
	if err := validateAgent("review", cfg.Agents.Review); err != nil {
		return nil, err
	}

	// Probe defaults to coding agent if omitted (HND-2)
	if cfg.Agents.Probe == "" {
		cfg.Agents.Probe = cfg.Agents.Coding
	}

	// Apply GitHub defaults and validate fields (GHB-1, PRJ-4, Decision P3)
	if cfg.GitHub.IntakeLabel == "" {
		cfg.GitHub.IntakeLabel = "garagefab"
	}
	if cfg.GitHub.PRIssueKeyword == "" {
		cfg.GitHub.PRIssueKeyword = "closes"
	}
	if cfg.GitHub.Repo != "" {
		parts := strings.Split(cfg.GitHub.Repo, "/")
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return nil, fmt.Errorf("config: project.yaml: github.repo: invalid repository format %q (want owner/repo)", cfg.GitHub.Repo)
		}
	}
	if cfg.GitHub.PRIssueKeyword != "closes" && cfg.GitHub.PRIssueKeyword != "refs" {
		return nil, fmt.Errorf("config: project.yaml: github.pr_issue_keyword: invalid keyword %q (want closes|refs)", cfg.GitHub.PRIssueKeyword)
	}

	return cfg, nil
}

// AgentForRole returns the configured agent name for a given SDLC role.
// If probe is unset, it automatically falls back to the coding agent (HND-2).
func (p *ProjectYAML) AgentForRole(role string) string {
	if p == nil {
		return ""
	}
	switch role {
	case "spec":
		return p.Agents.Spec
	case "probe":
		if p.Agents.Probe != "" {
			return p.Agents.Probe
		}
		return p.Agents.Coding
	case "coding":
		return p.Agents.Coding
	case "review":
		return p.Agents.Review
	default:
		return ""
	}
}
