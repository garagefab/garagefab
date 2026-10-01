package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/garagefab/garagefab/internal/config"
	"github.com/garagefab/garagefab/internal/store"
)

func TestCLI_Status_CLI4(t *testing.T) {
	tempDir := t.TempDir()

	// Initialize config
	cfg, err := config.Load(tempDir)
	if err != nil {
		t.Fatalf("config.Load failed: %v", err)
	}

	// Seed database
	dbPath := filepath.Join(cfg.DataDir, "garagefab.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open failed: %v", err)
	}

	p := &store.Project{Name: "my-service", RepoPath: "/tmp/my-service"}
	if err := db.Projects().CreateProject(context.Background(), p); err != nil {
		t.Fatalf("CreateProject failed: %v", err)
	}

	j := &store.Job{
		ProjectID: p.ID,
		WorkType:  store.WorkTypeRefactor,
		Title:     "Refactor cache layer",
		Stage:     store.StageCoding,
		Status:    store.StatusRunning,
	}
	if err := db.Jobs().CreateJob(context.Background(), j); err != nil {
		t.Fatalf("CreateJob failed: %v", err)
	}
	db.Close()

	// Execute statusCmd directly
	dataDirFlag = tempDir
	defer func() { dataDirFlag = "" }()

	buf := new(bytes.Buffer)
	statusCmd.SetOut(buf)

	err = statusCmd.RunE(statusCmd, []string{})
	if err != nil {
		t.Fatalf("statusCmd.RunE failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "my-service") ||
		!strings.Contains(out, "04_Coding") ||
		!strings.Contains(out, "running") ||
		!strings.Contains(out, "Refactor cache layer") {
		t.Errorf("CLI-4 violated: expected status table to contain job details, got:\n%s", out)
	}
}
