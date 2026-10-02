// Package store implements repository data access for dashboard overview metrics.
//
// ==============================================================================
// ARCHITECTURAL ROLE & ENTERPRISE / JAVA SPRING COMPARISON:
// Dashboard Metrics Aggregation & Attention Reporting (UI-1).
//
// In Clean / Hexagonal Architecture:
// `overview_repo.go` is an Outbound / Driven Adapter implementing optimized read-model
// projections for the dashboard Overview page. Instead of making multiple round-trips
// or hydrating full entity graphs, it issues targeted aggregate SQL queries.
//
// Enterprise / Spring Boot Comparison:
// Analogous to a dedicated Spring Data `@Repository` with custom `@Query` native projection
// methods returning a DTO (e.g., `OverviewDTO`).
//
// ==============================================================================
package store

import (
	"context"
	"fmt"
	"time"
)

// AttentionItem represents a high-priority job requiring human intervention (UI-1).
type AttentionItem struct {
	ID          int64     `json:"id"`
	ProjectID   int64     `json:"project_id"`
	ProjectName string    `json:"project_name"`
	WorkType    string    `json:"work_type"`
	Title       string    `json:"title"`
	Stage       string    `json:"stage"`
	Status      string    `json:"status"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// RecentActivityEvent represents an audit event formatted for the overview activity feed.
type RecentActivityEvent struct {
	ID        int64     `json:"id"`
	JobID     int64     `json:"job_id"`
	JobTitle  string    `json:"job_title"`
	Type      string    `json:"type"`
	Payload   string    `json:"payload"`
	CreatedAt time.Time `json:"created_at"`
}

// OverviewData aggregates all statistics and lists for the Overview dashboard (UI-1).
type OverviewData struct {
	JobCounts       map[string]int         `json:"job_counts"`
	AttentionList   []*AttentionItem       `json:"attention_list"`
	RecentActivity  []*RecentActivityEvent `json:"recent_activity"`
	OrphanWorktrees []string               `json:"orphan_worktrees"`
	IntakeErrors    []string               `json:"intake_errors"`
}

// OverviewRepo provides read projections for dashboard overview data.
type OverviewRepo struct {
	q dbtx
}

// GetOverviewData executes aggregate queries and returns complete dashboard overview data (UI-1).
func (r *OverviewRepo) GetOverviewData(ctx context.Context) (*OverviewData, error) {
	data := &OverviewData{
		JobCounts: map[string]int{
			StatusQueued:             0,
			StatusRunning:            0,
			StatusNeedsClarification: 0,
			StatusSpecReview:         0,
			StatusAwaitingApproval:   0,
			StatusDone:               0,
			StatusFailed:             0,
			StatusInterrupted:        0,
			StatusCancelled:          0,
		},
		AttentionList:   []*AttentionItem{},
		RecentActivity:  []*RecentActivityEvent{},
		OrphanWorktrees: []string{},
		IntakeErrors:    []string{},
	}

	// 1. Aggregate status counts across all non-archived projects
	countQuery := `
		SELECT j.status, COUNT(j.id)
		FROM jobs j
		JOIN projects p ON j.project_id = p.id
		WHERE p.is_archived = 0
		GROUP BY j.status
	`
	rows, err := r.q.QueryContext(ctx, countQuery)
	if err != nil {
		return nil, fmt.Errorf("store: query overview job counts: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, fmt.Errorf("store: scan overview job count: %w", err)
		}
		data.JobCounts[status] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: rows overview job counts: %w", err)
	}

	// 2. Query attention list: jobs needing developer intervention (UI-1)
	attentionQuery := `
		SELECT j.id, j.project_id, p.name, j.work_type, j.title, j.stage, j.status, j.updated_at
		FROM jobs j
		JOIN projects p ON j.project_id = p.id
		WHERE p.is_archived = 0 AND j.status IN ('needs_clarification', 'spec_review', 'awaiting_approval', 'failed', 'interrupted')
		ORDER BY j.updated_at DESC
	`
	attRows, err := r.q.QueryContext(ctx, attentionQuery)
	if err != nil {
		return nil, fmt.Errorf("store: query overview attention list: %w", err)
	}
	defer attRows.Close()

	for attRows.Next() {
		var item AttentionItem
		var updatedStr string
		if err := attRows.Scan(&item.ID, &item.ProjectID, &item.ProjectName, &item.WorkType, &item.Title, &item.Stage, &item.Status, &updatedStr); err != nil {
			return nil, fmt.Errorf("store: scan overview attention item: %w", err)
		}
		item.UpdatedAt = parseTime(updatedStr)
		data.AttentionList = append(data.AttentionList, &item)
	}
	if err := attRows.Err(); err != nil {
		return nil, fmt.Errorf("store: rows overview attention list: %w", err)
	}

	// 3. Query recent activity feed (last 15 events joined with job title)
	activityQuery := `
		SELECT e.id, e.job_id, COALESCE(j.title, ''), e.type, e.payload, e.created_at
		FROM events e
		LEFT JOIN jobs j ON e.job_id = j.id
		ORDER BY e.id DESC
		LIMIT 15
	`
	actRows, err := r.q.QueryContext(ctx, activityQuery)
	if err != nil {
		return nil, fmt.Errorf("store: query overview recent activity: %w", err)
	}
	defer actRows.Close()

	for actRows.Next() {
		var ev RecentActivityEvent
		var createdStr string
		if err := actRows.Scan(&ev.ID, &ev.JobID, &ev.JobTitle, &ev.Type, &ev.Payload, &createdStr); err != nil {
			return nil, fmt.Errorf("store: scan overview recent event: %w", err)
		}
		ev.CreatedAt = parseTime(createdStr)
		data.RecentActivity = append(data.RecentActivity, &ev)
	}
	if err := actRows.Err(); err != nil {
		return nil, fmt.Errorf("store: rows overview recent activity: %w", err)
	}

	return data, nil
}
