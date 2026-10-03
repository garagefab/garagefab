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

// ProjectYAML defines the schema for `<repo>/.garagefab/project.yaml`.
type ProjectYAML struct {
	BaseRef  string          `yaml:"base_ref"`
	Agents   ProjectAgents   `yaml:"agents"`
	Timeouts ProjectTimeouts `yaml:"timeouts"`
	Commands struct {
		Build []string `yaml:"build"`
		Test  []string `yaml:"test"`
		Lint  []string `yaml:"lint"`
	} `yaml:"commands"`
	Guardrails struct {
		ProtectedPaths []string `yaml:"protected_paths"`
		Commands       []string `yaml:"commands"`
	} `yaml:"guardrails"`
	MaxConcurrentJobs int `yaml:"max_concurrent_jobs"`
}

// LoadProjectConfig reads and parses `<repo>/.garagefab/project.yaml`.
// If the file does not exist, it returns a default configuration with empty commands
// and standard test path protection (`**/*_test.go`).
func LoadProjectConfig(repoPath string) (*ProjectYAML, error) {
	cfg := &ProjectYAML{
		BaseRef: "origin/main",
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
