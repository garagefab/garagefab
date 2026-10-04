// Package intake manages external task ingestion from intent files and GitHub issues.
//
// ==============================================================================
// ARCHITECTURAL ROLE & PORTS:
// Driving Ingestion Adapter Ports (Hexagonal Architecture).
//
// In Clean / Hexagonal Architecture:
// `internal/intake` is a driving adapter. It imports `store` for persistence
// and defines consumer-side interfaces (ports) for dependencies it needs:
//   - `IssueSource`: Outbound port to fetch GitHub trigger issues (implemented by github.Client via adapter).
//   - `SchedulerNotifier`: Outbound port to wake the job scheduler upon job creation.
//
// ENTERPRISE / JAVA SPRING COMPARISON:
//   - Spring `@Scheduled` Ingestion Worker: Similar to an enterprise scheduled service
//     polling multiple datasources (file system directories and external issue trackers)
//     and transforming raw events into transactional domain jobs.
//   - Decoupled Integration Port: Replaces hard dependencies on external SDKs with
//     clean Go interfaces (`IssueSource`).
//
// GO IDIOMS & CONCEPTS:
//   1. Consumer-Side Interface Segregation:
//      Interfaces are defined where they are consumed (`internal/intake`), not where
//      they are implemented (`internal/provider/github`).
// ==============================================================================
package intake

import "context"

// IssueDTO represents an open issue returned from the issue provider (INT-3).
type IssueDTO struct {
	Number int
	Title  string
	Body   string
	Labels []string
	URL    string
}

// IssueSource defines the outbound port for querying candidate GitHub issues.
type IssueSource interface {
	ListTriggerIssues(ctx context.Context, repo, label string) ([]IssueDTO, error)
}

// SchedulerNotifier defines the outbound port to wake the job scheduler immediately (INT-7).
type SchedulerNotifier interface {
	Wake()
}
