// Package main provides adapters bridging GitHub provider operations to factory and intake ports.
//
// ==============================================================================
// ARCHITECTURAL ROLE & ENTERPRISE/JAVA BRIDGE:
// Composition Root Adapters & Anti-Corruption Layer (Hexagonal Architecture).
//
// In Clean / Hexagonal Architecture:
// `github_adapters.go` acts as an Anti-Corruption Layer (ACL) in the Composition Root.
// It bridges the concrete `provider/github.Client` to the domain and driving ports:
//  1. `factoryPullRequestAdapter`: Implements `factory.PullRequestProvider` (DLV-1, DLV-2).
//  2. `intakeIssueSourceAdapter`: Implements `intake.IssueSource` (INT-3).
//  3. `intakeIssueFeedbackAdapter`: Implements `intake.IssueFeedback` (GHB-2, GHB-5).
//
// Dependency Rule Enforcement:
// - Under Rule 1 (`factory` never imports `provider`) and Rule 6 (`provider` never
//   imports internal packages), domain and provider packages have zero awareness of each other.
// - This file in `cmd/garagefab` is the single place where concrete DTOs are mapped across boundaries.
//
// JAVA / SPRING BOOT COMPARISON:
// - Corresponds to Spring `@Configuration` adapter beans mapping third-party Feign/REST
//   DTOs into domain outbound port interfaces, preventing third-party library contamination
//   of the core business domain.
//
// GO IDIOMS & CONCEPTS:
// 1. Structural Subtyping:
//    Adapters implicitly satisfy interfaces declared in `factory` and `intake` without
//    explicit `implements` keywords.
// 2. Explicit DTO Mapping:
//    Uses explicit struct conversion loops rather than reflection or magic mappers,
//    ensuring compile-time safety and zero hidden allocations.
// ==============================================================================
package main

import (
	"context"

	"github.com/garagefab/garagefab/internal/factory"
	"github.com/garagefab/garagefab/internal/intake"
	"github.com/garagefab/garagefab/internal/provider/github"
)

// ------------------------------------------------------------------------------
// Factory Pull Request Adapter (DLV-1, DLV-2)
// ------------------------------------------------------------------------------

type factoryPullRequestAdapter struct {
	client *github.Client
}

func newFactoryPullRequestAdapter(client *github.Client) factory.PullRequestProvider {
	return &factoryPullRequestAdapter{client: client}
}

func (a *factoryPullRequestAdapter) FindPullRequest(ctx context.Context, repo, head string) (*factory.PullRequest, error) {
	pr, err := a.client.FindPR(ctx, repo, head)
	if err != nil {
		return nil, err
	}
	if pr == nil {
		return nil, nil
	}
	return &factory.PullRequest{
		Number: pr.Number,
		URL:    pr.URL,
		State:  pr.State,
	}, nil
}

func (a *factoryPullRequestAdapter) CreatePullRequest(ctx context.Context, req factory.PullRequestRequest) (*factory.PullRequest, error) {
	pr, err := a.client.CreatePR(ctx, req.Repo, req.Base, req.Head, req.Title, req.Body)
	if err != nil {
		return nil, err
	}
	return &factory.PullRequest{
		Number: pr.Number,
		URL:    pr.URL,
		State:  pr.State,
	}, nil
}

// ------------------------------------------------------------------------------
// Intake Issue Source Adapter (INT-3)
// ------------------------------------------------------------------------------

type intakeIssueSourceAdapter struct {
	client *github.Client
}

func newIntakeIssueSourceAdapter(client *github.Client) intake.IssueSource {
	return &intakeIssueSourceAdapter{client: client}
}

func (a *intakeIssueSourceAdapter) ListTriggerIssues(ctx context.Context, repo, label string) ([]intake.IssueDTO, error) {
	issues, err := a.client.ListIssues(ctx, repo, label)
	if err != nil {
		return nil, err
	}
	dtos := make([]intake.IssueDTO, len(issues))
	for i, iss := range issues {
		dtos[i] = intake.IssueDTO{
			Number: iss.Number,
			Title:  iss.Title,
			Body:   iss.Body,
			Labels: iss.Labels,
			URL:    iss.URL,
		}
	}
	return dtos, nil
}

// ------------------------------------------------------------------------------
// Intake Issue Feedback Adapter (GHB-2, GHB-5)
// ------------------------------------------------------------------------------

type intakeIssueFeedbackAdapter struct {
	client *github.Client
}

func newIntakeIssueFeedbackAdapter(client *github.Client) intake.IssueFeedback {
	return &intakeIssueFeedbackAdapter{client: client}
}

func (a *intakeIssueFeedbackAdapter) EnsureLabels(ctx context.Context, repo string, labels []intake.LabelSpec) error {
	for _, l := range labels {
		if err := a.client.EnsureLabel(ctx, repo, l.Name, l.Color, l.Description); err != nil {
			return err
		}
	}
	return nil
}

func (a *intakeIssueFeedbackAdapter) SetLabels(ctx context.Context, repo string, issueNum int, add, remove []string) error {
	return a.client.EditLabels(ctx, repo, issueNum, add, remove)
}

func (a *intakeIssueFeedbackAdapter) Comment(ctx context.Context, repo string, issueNum int, body string) error {
	return a.client.Comment(ctx, repo, issueNum, body)
}
