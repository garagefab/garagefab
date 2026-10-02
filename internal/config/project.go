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

	"gopkg.in/yaml.v3"
)

// ProjectYAML defines the schema for `<repo>/.garagefab/project.yaml`.
type ProjectYAML struct {
	BaseRef  string `yaml:"base_ref"`
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

	return cfg, nil
}
