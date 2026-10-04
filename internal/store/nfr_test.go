// Package store_test contains NFR verification tests (NFR-4, NFR-6, NFR-7).
//
// ==============================================================================
// ARCHITECTURAL ROLE:
// Non-Functional Requirement Verification for the persistence layer.
//
//   - NFR-4: overview queries are bounded, so a large history cannot blow up a response.
//   - NFR-6: a hard crash (kill -9) loses no committed state.
//   - NFR-7: five concurrent job writers plus a parallel reader never see "database is locked".
//
// ==============================================================================
package store_test

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/garagefab/garagefab/internal/store"
)

// TestOverviewQueries_Bounded_NFR4 verifies that the overview attention list and intake errors
// are capped even when far more rows exist (NFR-4).
func TestOverviewQueries_Bounded_NFR4(t *testing.T) {
	ctx := context.Background()
	db := setupTestDB(t)

	proj := &store.Project{Name: "p", RepoPath: "/tmp/p", BaseRef: "main"}
	if err := db.Projects().CreateProject(ctx, proj); err != nil {
		t.Fatalf("CreateProject failed: %v", err)
	}

	for i := 0; i < 120; i++ {
		j := &store.Job{
			ProjectID: proj.ID,
			WorkType:  store.WorkTypeRefactor,
			Title:     fmt.Sprintf("failed-%d", i),
			Intent:    "x",
			Stage:     store.StageCoding,
			Status:    store.StatusFailed,
		}
		if err := db.Jobs().CreateJob(ctx, j); err != nil {
			t.Fatalf("CreateJob failed: %v", err)
		}
	}
	for i := 0; i < 120; i++ {
		if err := db.Intake().UpsertIntakeError(ctx, &store.IntakeError{
			ProjectID: proj.ID,
			Source:    "intent_file",
			Ref:       fmt.Sprintf("ref-%d", i),
			Message:   fmt.Sprintf("error-%d", i),
			UpdatedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("UpsertIntakeError failed: %v", err)
		}
	}

	data, err := db.Overview().GetOverviewData(ctx)
	if err != nil {
		t.Fatalf("GetOverviewData failed: %v", err)
	}
	if len(data.AttentionList) > 50 {
		t.Fatalf("attention list not bounded: %d", len(data.AttentionList))
	}
	if len(data.IntakeErrors) > 50 {
		t.Fatalf("intake errors not bounded: %d", len(data.IntakeErrors))
	}
}

// TestConcurrentJobs_ParallelReader_NFR7 drives five concurrent job writers through the
// repository layer while a reader polls, asserting no "database is locked" (NFR-7).
func TestConcurrentJobs_ParallelReader_NFR7(t *testing.T) {
	ctx := context.Background()
	db := setupTestDB(t)

	proj := &store.Project{Name: "p", RepoPath: "/tmp/p", BaseRef: "main"}
	if err := db.Projects().CreateProject(ctx, proj); err != nil {
		t.Fatalf("CreateProject failed: %v", err)
	}

	const writers = 5
	const iterations = 20
	errCh := make(chan error, writers+1)
	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Parallel reader.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := db.Jobs().ListJobs(ctx, store.JobListFilter{Limit: 10}); err != nil {
				errCh <- err
				return
			}
			if _, err := db.Jobs().CountJobsByStatus(ctx); err != nil {
				errCh <- err
				return
			}
		}
	}()

	// Concurrent job writers.
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				j := &store.Job{
					ProjectID: proj.ID,
					WorkType:  store.WorkTypeRefactor,
					Title:     fmt.Sprintf("w%d-%d", w, i),
					Intent:    "x",
					Stage:     store.StageCoding,
					Status:    store.StatusRunning,
				}
				if err := db.Jobs().CreateJob(ctx, j); err != nil {
					errCh <- err
					return
				}
				if err := db.Jobs().UpdateJobState(ctx, j.ID, store.StageCoding, store.StatusDone); err != nil {
					errCh <- err
					return
				}
			}
		}(w)
	}

	// Wait for writers to finish, then stop the reader.
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	// Give writers time; the reader runs until stop is closed.
	time.Sleep(200 * time.Millisecond)
	close(stop)
	<-done

	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("NFR-7 violation: %v", err)
		}
	}
}

// TestCrash_Kill9_NoCommittedStateLost_NFR6 kills a writer process mid-write and verifies that
// committed state survives and the database still passes integrity_check (NFR-6).
func TestCrash_Kill9_NoCommittedStateLost_NFR6(t *testing.T) {
	if os.Getenv("GARAGEFAB_CRASH_CHILD") == "1" {
		runCrashChild()
		return
	}

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "crash.db")
	marker := fmt.Sprintf("crash-marker-%d", time.Now().UnixNano())

	cmd := exec.Command(os.Args[0], "-test.run=TestCrash_Kill9_NoCommittedStateLost_NFR6")
	cmd.Env = append(os.Environ(),
		"GARAGEFAB_CRASH_CHILD=1",
		"GARAGEFAB_CRASH_DB="+dbPath,
		"GARAGEFAB_CRASH_TITLE="+marker,
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}

	reader := bufio.NewReader(stdout)
	// Wait for the child to signal that the marker job is committed.
	if line, err := reader.ReadString('\n'); err != nil || !strings.Contains(line, "READY") {
		_ = cmd.Process.Kill()
		t.Fatalf("child did not signal READY: line=%q err=%v", line, err)
	}

	// Hard kill mid-write.
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill child: %v", err)
	}
	_ = cmd.Wait()

	// Reopen and verify committed state + integrity.
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("reopen after crash failed: %v", err)
	}
	defer db.Close()

	var integrity string
	if err := db.QueryRowContext(context.Background(), "PRAGMA integrity_check").Scan(&integrity); err != nil {
		t.Fatalf("integrity_check failed: %v", err)
	}
	if integrity != "ok" {
		t.Fatalf("integrity_check returned %q", integrity)
	}

	jobs, err := db.Jobs().ListJobs(context.Background(), store.JobListFilter{})
	if err != nil {
		t.Fatalf("ListJobs after crash failed: %v", err)
	}
	var found bool
	for _, j := range jobs {
		if j.Title == marker {
			found = true
		}
	}
	if !found {
		t.Fatalf("committed job %q was lost after kill -9", marker)
	}
}

// runCrashChild commits a marker job and then writes continuously until it is killed.
func runCrashChild() {
	ctx := context.Background()
	db, err := store.Open(os.Getenv("GARAGEFAB_CRASH_DB"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "crash child: open store:", err)
		os.Exit(2)
	}
	proj := &store.Project{Name: "crash", RepoPath: "/tmp/crash", BaseRef: "main"}
	if err := db.Projects().CreateProject(ctx, proj); err != nil {
		os.Exit(2)
	}
	job := &store.Job{
		ProjectID: proj.ID,
		WorkType:  store.WorkTypeRefactor,
		Title:     os.Getenv("GARAGEFAB_CRASH_TITLE"),
		Intent:    "x",
		Stage:     store.StageIntent,
		Status:    store.StatusQueued,
	}
	if err := db.Jobs().CreateJob(ctx, job); err != nil {
		os.Exit(2)
	}

	fmt.Println("READY")
	for i := 0; ; i++ {
		// Keep the WAL busy so the kill lands mid-write.
		_, _ = db.ExecContext(ctx,
			"INSERT INTO events (job_id, type, payload, created_at) VALUES (?, ?, ?, ?)",
			job.ID, "crash.busy", fmt.Sprintf("tick-%d", i), time.Now().UTC().Format(time.RFC3339Nano))
	}
}
