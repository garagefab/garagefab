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
