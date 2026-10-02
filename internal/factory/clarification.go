// Package factory implements domain models, validators, and pipeline orchestration.
//
// ==============================================================================
// ARCHITECTURAL ROLE & USE CASE:
// Application Service Method: Clarification Answer Submission (SPC-3, spec §6.4).
//
// Role:
// Coordinates receiving clarification answers from the human engineer or external
// CLI/agent, assembling the question-and-answer record (`clarification.md`), removing
// pending questions (`clarification-questions.md`), writing a Git checkpoint commit,
// and transitioning the job back to `02_Clarification_and_Spec / queued`.
//
// ENTERPRISE & JAVA / SPRING COMPARISON:
// In Spring Boot: This corresponds to a transactional service method (`@Transactional void submitClarification(...)`)
// in `JobService`.
// In Go: The engine coordinates domain ports (`WorktreeManager`, `Store.InTx`) explicitly,
// guaranteeing atomicity between the Git worktree checkpoint and SQLite state update.
//
// ==============================================================================
package factory

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// ClarificationAnswer represents a single answer mapped to a question index (SPC-3).
type ClarificationAnswer struct {
	Q      int    `json:"q"`
	Answer string `json:"answer"`
}

var (
	// ErrNoClarificationAnswers is returned when an empty answer list is submitted.
	ErrNoClarificationAnswers = errors.New("factory: answers list cannot be empty")
	// ErrInvalidQuestionIndex is returned when question index Q is non-positive.
	ErrInvalidQuestionIndex = errors.New("factory: question index Q must be >= 1")
	// ErrEmptyAnswerText is returned when an answer string contains only whitespace.
	ErrEmptyAnswerText = errors.New("factory: answer text cannot be empty")
)

var reQuestionLine = regexp.MustCompile(`^(?i)Q(\d+)[\.:\)]\s*(.*)$`)

// SubmitClarification records clarification answers for a job awaiting clarification (SPC-3).
//
// Steps:
// 1. Invariant check: Job must be in StageClarificationAndSpec / StatusNeedsClarification.
// 2. Read clarification-questions.md from worktree to extract question prompts.
// 3. Format clarification.md adhering to spec §6.4.
// 4. Delete clarification-questions.md (present only while pending, SPC-1).
// 5. Commit Git checkpoint: 'garagefab(job-<id>): clarification answers'.
// 6. Transition job status back to StatusQueued in StageClarificationAndSpec and wake scheduler.
func (e *Engine) SubmitClarification(ctx context.Context, jobID int64, answers []ClarificationAnswer) error {
	if len(answers) == 0 {
		return ErrNoClarificationAnswers
	}
	for _, a := range answers {
		if a.Q < 1 {
			return ErrInvalidQuestionIndex
		}
		if strings.TrimSpace(a.Answer) == "" {
			return ErrEmptyAnswerText
		}
	}

	job, err := e.store.GetJob(ctx, jobID)
	if err != nil {
		return fmt.Errorf("factory: get job %d: %w", jobID, err)
	}

	// Validate job stage and status
	if job.Stage != StageClarificationAndSpec || job.Status != StatusNeedsClarification {
		return fmt.Errorf("%w: job %d is in %s/%s, expected %s/%s",
			ErrInvalidState, jobID, job.Stage, job.Status, StageClarificationAndSpec, StatusNeedsClarification)
	}

	// Read questions file if present
	questionsMap := make(map[int]string)
	qBytes, err := e.wtMgr.ReadArtifact(ctx, job.WorktreePath, job.ID, "clarification-questions.md")
	if err == nil && len(qBytes) > 0 {
		scanner := bufio.NewScanner(strings.NewReader(string(qBytes)))
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if m := reQuestionLine.FindStringSubmatch(line); len(m) > 2 {
				qNum, _ := strconv.Atoi(m[1])
				questionsMap[qNum] = strings.TrimSpace(m[2])
			}
		}
	}

	// Read existing clarification.md content if any
	var sb strings.Builder
	existingData, err := e.wtMgr.ReadArtifact(ctx, job.WorktreePath, job.ID, "clarification.md")
	if err == nil && len(existingData) > 0 {
		sb.Write(existingData)
		if !strings.HasSuffix(sb.String(), "\n\n") {
			sb.WriteString("\n\n")
		}
	}

	// Format Q&A blocks per spec §6.4:
	// ## Q1
	// <question text>
	// **Answer:** <answer text>
	for _, a := range answers {
		qText := questionsMap[a.Q]
		if qText == "" {
			qText = fmt.Sprintf("Question %d", a.Q)
		}
		fmt.Fprintf(&sb, "## Q%d\n%s\n**Answer:** %s\n\n", a.Q, qText, strings.TrimSpace(a.Answer))
	}

	// Write clarification.md
	if err := e.wtMgr.WriteArtifact(ctx, job.WorktreePath, job.ID, "clarification.md", []byte(sb.String())); err != nil {
		return fmt.Errorf("factory: write clarification.md: %w", err)
	}

	// Remove clarification-questions.md (SPC-1, present only while clarification is pending)
	_ = e.wtMgr.RemoveArtifact(ctx, job.WorktreePath, job.ID, "clarification-questions.md")

	// Commit Git checkpoint
	headSHA, err := e.wtMgr.Checkpoint(ctx, job.WorktreePath, job.ID, "clarification answers")
	if err != nil {
		return fmt.Errorf("factory: clarification checkpoint: %w", err)
	}
	job.HeadSHA = headSHA

	// Transition job status back to queued in StageClarificationAndSpec
	err = e.store.InTx(ctx, func(tx StoreTx) error {
		if err := tx.UpdateJobHead(ctx, job.ID, headSHA); err != nil {
			return err
		}
		if err := tx.UpdateJobState(ctx, job.ID, StageClarificationAndSpec, StatusQueued); err != nil {
			return err
		}
		return tx.RecordEvent(ctx, job.ID, "job.status_changed", fmt.Sprintf(`{"stage":%q,"status":%q}`, StageClarificationAndSpec, StatusQueued))
	})
	if err != nil {
		return fmt.Errorf("factory: update job status after clarification: %w", err)
	}

	job.Status = StatusQueued
	e.notifyWake()
	return nil
}
