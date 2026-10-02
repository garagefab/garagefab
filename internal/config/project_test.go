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
