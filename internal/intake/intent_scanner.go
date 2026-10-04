// Package intake provides intent file scanning and automated job creation.
//
// ==============================================================================
// ARCHITECTURAL ROLE & PATTERNS:
// Intent File Scanner & Idempotent Ingestion (INT-2, INT-4, INT-7).
//
// In Clean / Hexagonal Architecture:
// `intent_scanner.go` inspects local Git repositories for markdown intent files
// matching `<repo>/.garagefab/intents/*-intent.md`.
//
// Invariant Pipeline Invariants:
//  1. YAML Front Matter Validation: Every intent file must declare a valid `type:`
//     (`bug_fix`, `feature`, `refactor`, or `docs`). Files without valid front matter
//     are logged as intake errors on the overview page and skipped.
//  2. Atomic Ingestion (INT-4): Job record, creation event, and idempotency seen
//     marker are written within a single database transaction (`db.CreateJobFromIntake`).
//  3. Reactive Notification (INT-7): Wakes the factory scheduler immediately when
//     a new job is admitted.
//
// ENTERPRISE / JAVA SPRING COMPARISON:
//   - File Polling Adapter: Equivalent to Spring Integration `FileReadingMessageSource`
//     polling a directory, extracting metadata, and routing messages.
//
// GO IDIOMS & CONCEPTS:
//  1. Path Normalization:
//     Converts OS-specific backslashes to standard forward slashes (`filepath.ToSlash`)
//     so source references are consistent across macOS, Linux, and Windows.
//  2. Sha256 Fingerprinting:
//     Hashes file contents to detect modifications or record provenance in `intake_seen`.
//
// ==============================================================================
package intake

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/garagefab/garagefab/internal/store"
	"gopkg.in/yaml.v3"
)

// IntentFrontMatter captures metadata declared in the YAML header of an intent file.
type IntentFrontMatter struct {
	Type  string `yaml:"type"`            // bug_fix | feature | refactor | docs
	Title string `yaml:"title,omitempty"` // Optional human-readable title
}

// parseIntentFile parses YAML front matter and separates it from the markdown body.
func parseIntentFile(content []byte) (*IntentFrontMatter, string, error) {
	str := string(content)
	if !strings.HasPrefix(strings.TrimSpace(str), "---") {
		return nil, "", fmt.Errorf("missing opening '---' front matter delimiter")
	}

	trimmed := strings.TrimLeft(str, " \t\r\n")
	parts := strings.SplitN(trimmed[3:], "---", 2)
	if len(parts) < 2 {
		return nil, "", fmt.Errorf("missing closing '---' front matter delimiter")
	}

	var fm IntentFrontMatter
	if err := yaml.Unmarshal([]byte(parts[0]), &fm); err != nil {
		return nil, "", fmt.Errorf("parse front matter: %w", err)
	}

	body := strings.TrimSpace(parts[1])
	return &fm, body, nil
}

// isValidWorkType checks if the given type string is a recognized SDLC work type.
func isValidWorkType(t string) bool {
	switch t {
	case store.WorkTypeBugFix, store.WorkTypeFeature, store.WorkTypeRefactor, store.WorkTypeDocs:
		return true
	default:
		return false
	}
}

// ScanProjectIntents inspects <repo>/.garagefab/intents/*-intent.md and ingests new jobs (INT-2).
func ScanProjectIntents(ctx context.Context, db *store.DB, project *store.Project, notifier SchedulerNotifier) error {
	intentsPattern := filepath.Join(project.RepoPath, ".garagefab", "intents", "*-intent.md")
	matches, err := filepath.Glob(intentsPattern)
	if err != nil {
		return fmt.Errorf("intake: glob intent files: %w", err)
	}

	for _, fullPath := range matches {
		relPath, relErr := filepath.Rel(project.RepoPath, fullPath)
		if relErr != nil {
			relPath = fullPath
		}
		relPath = filepath.ToSlash(relPath)

		// Check if already seen (INT-4 idempotency)
		seenAlready, err := db.Intake().IsSeen(ctx, project.ID, store.SourceIntentFile, relPath)
		if err != nil {
			slog.Warn("intake: check is_seen failed", "project", project.Name, "path", relPath, "error", err)
			continue
		}
		if seenAlready {
			continue
		}

		data, err := os.ReadFile(fullPath)
		if err != nil {
			slog.Warn("intake: read intent file failed", "project", project.Name, "path", relPath, "error", err)
			continue
		}

		fm, body, err := parseIntentFile(data)
		if err != nil || !isValidWorkType(fm.Type) {
			errMsg := fmt.Sprintf("invalid intent file %s: missing or invalid 'type:' (must be bug_fix|feature|refactor|docs)", filepath.Base(relPath))
			if err != nil {
				errMsg = fmt.Sprintf("invalid intent file %s: %v", filepath.Base(relPath), err)
			}
			_ = db.Intake().UpsertIntakeError(ctx, &store.IntakeError{
				ProjectID: project.ID,
				Source:    store.SourceIntentFile,
				Ref:       relPath,
				Message:   errMsg,
			})
			continue
		}

		// Derive title from front matter or humanized filename
		title := strings.TrimSpace(fm.Title)
		if title == "" {
			base := filepath.Base(relPath)
			base = strings.TrimSuffix(base, "-intent.md")
			title = strings.ReplaceAll(base, "-", " ")
			title = strings.ReplaceAll(title, "_", " ")
			if len(title) > 0 {
				title = strings.ToUpper(title[:1]) + title[1:]
			}
		}

		// Calculate SHA-256 hash of content
		hash := sha256.Sum256(data)
		contentHash := hex.EncodeToString(hash[:])

		job := &store.Job{
			ProjectID: project.ID,
			WorkType:  fm.Type,
			Title:     title,
			Intent:    body,
			Source:    store.SourceIntentFile,
			SourceRef: relPath,
		}
		seen := &store.IntakeSeen{
			ProjectID:   project.ID,
			Source:      store.SourceIntentFile,
			Ref:         relPath,
			ContentHash: contentHash,
		}

		if err := db.CreateJobFromIntake(ctx, job, seen); err != nil {
			slog.Error("intake: create job from intent file failed", "project", project.Name, "file", relPath, "error", err)
			continue
		}

		// Clear prior error on clean success
		_ = db.Intake().ClearIntakeError(ctx, project.ID, store.SourceIntentFile, relPath)

		slog.Info("intake: created job from intent file", "job_id", job.ID, "project", project.Name, "work_type", job.WorkType, "title", job.Title)

		// Wake scheduler immediately (INT-7)
		if notifier != nil {
			notifier.Wake()
		}
	}

	return nil
}
