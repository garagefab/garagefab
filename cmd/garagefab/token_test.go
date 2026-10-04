// Package main provides CLI command definitions and verification tests.
//
// ==============================================================================
// ARCHITECTURAL ROLE & TESTING CONCEPTS:
// API Credential Rotation Verification (CLI-8).
//
// Verifies that `RunTokenRotate`:
//  1. Writes a brand-new API token to config.yaml with mode 0600.
//  2. Empties the sessions table so old cookies/bearers are rejected after restart.
//  3. Refuses to rotate while a daemon holds the single-instance lock, unless --force.
//
// ==============================================================================
package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/garagefab/garagefab/internal/config"
	"github.com/garagefab/garagefab/internal/store"
)

// TestTokenRotate_InvalidatesSessionsAndBearer_CLI8 verifies requirement CLI-8.
func TestTokenRotate_InvalidatesSessionsAndBearer_CLI8(t *testing.T) {
	dir := t.TempDir()
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatalf("config.Load failed: %v", err)
	}
	oldToken := cfg.Server.APIToken

	// Seed a dashboard session.
	db, err := store.Open(filepath.Join(dir, "garagefab.db"))
	if err != nil {
		t.Fatalf("store.Open failed: %v", err)
	}
	if err := db.Sessions().CreateSession(context.Background(), &store.Session{
		ID:        "sess-1",
		TokenHash: "hash",
		ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	_ = db.Close()

	var out bytes.Buffer
	if err := RunTokenRotate(RunTokenRotateOptions{DataDir: dir, Out: &out}); err != nil {
		t.Fatalf("RunTokenRotate failed: %v", err)
	}

	// New token persisted.
	cfgAfter, err := config.Load(dir)
	if err != nil {
		t.Fatalf("config.Load after rotate failed: %v", err)
	}
	if cfgAfter.Server.APIToken == oldToken || cfgAfter.Server.APIToken == "" {
		t.Fatalf("expected a new token, old=%q new=%q", oldToken, cfgAfter.Server.APIToken)
	}

	// Session rows emptied.
	dbAfter, err := store.Open(filepath.Join(dir, "garagefab.db"))
	if err != nil {
		t.Fatalf("store.Open after rotate failed: %v", err)
	}
	defer dbAfter.Close()
	if _, err := dbAfter.Sessions().GetSession(context.Background(), "sess-1"); err == nil {
		t.Fatal("expected the seeded session to be deleted")
	}

	// Config file stays 0600.
	info, err := os.Stat(filepath.Join(dir, "config.yaml"))
	if err != nil {
		t.Fatalf("stat config.yaml failed: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Fatalf("expected config.yaml mode 0600, got %#o", perm)
	}
}

// TestTokenRotate_RefusesWhileRunning_CLI8 verifies requirement CLI-8.
func TestTokenRotate_RefusesWhileRunning_CLI8(t *testing.T) {
	dir := t.TempDir()
	if _, err := config.Load(dir); err != nil {
		t.Fatalf("config.Load failed: %v", err)
	}

	lock, err := config.AcquireLock(dir)
	if err != nil {
		t.Fatalf("AcquireLock failed: %v", err)
	}
	defer func() { _ = lock.Release() }()

	var out bytes.Buffer
	err = RunTokenRotate(RunTokenRotateOptions{DataDir: dir, Out: &out})
	if err == nil {
		t.Fatal("expected rotation to be refused while a daemon is running")
	}
	if !strings.Contains(err.Error(), "already running") {
		t.Fatalf("expected an 'already running' error, got: %v", err)
	}

	// --force rotates anyway.
	if err := RunTokenRotate(RunTokenRotateOptions{DataDir: dir, Force: true, Out: &out}); err != nil {
		t.Fatalf("forced rotation failed: %v", err)
	}
}
