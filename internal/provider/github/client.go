// Package github provides high-level client operations for GitHub integration.
//
// ==============================================================================
// ARCHITECTURAL ROLE & PATTERNS:
// Driven Adapter Client for GitHub Operations (Hexagonal Architecture).
//
// In Clean / Hexagonal Architecture:
// `client.go` encapsulates all interaction with the GitHub platform via the GitHub CLI ('gh').
//
// Boundary Rules (AGENTS.md Rule 6):
//   - ZERO internal package imports: client.go only imports standard library packages.
//   - Returns decoupled DTOs (IssueDTO, PullRequestDTO).
//   - Outbound and inbound ports in factory and intake are adapted in cmd/garagefab.
//
// ENTERPRISE / JAVA SPRING COMPARISON:
//   - FeignClient / RestClient Adapter: Similar to a Spring `@Service` wrapping an external
//     API client, mapping wire-format JSON models into clean application DTOs.
//   - Idempotency & Safety: Commands use `--force` for label creation, `--body-file -`
//     via stdin to avoid CLI argument length limits, and `--json` to eliminate fragile
//     text scraping.
//
// GO IDIOMS & CONCEPTS:
//   1. JSON Unmarshaling with Struct Tags:
//      Standard `encoding/json` maps CLI JSON output into Go structs.
//   2. Stdin Streaming via `[]byte`:
//      Large comment and PR descriptions are streamed via stdin (`--body-file -`),
//      avoiding shell escaping vulnerabilities and argument length limits.
// ==============================================================================
package github

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// IssueDTO represents an open issue ingested from GitHub (INT-3).
type IssueDTO struct {
	Number int      `json:"number"`
	Title  string   `json:"title"`
	Body   string   `json:"body"`
	Labels []string `json:"labels"`
	URL    string   `json:"url"`
}

// PullRequestDTO represents a pull request inspected or created via gh (DLV-1, DLV-2).
type PullRequestDTO struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
	State  string `json:"state"` // "OPEN", "CLOSED", "MERGED"
}

// LabelSpec defines properties for bootstrapping issue status labels (GHB-2).
type LabelSpec struct {
	Name        string
	Color       string
	Description string
}

// Client executes GitHub operations by invoking the mockable GHRunner.
type Client struct {
	runner GHRunner
}

// NewClient constructs a new GitHub client adapter.
func NewClient(runner GHRunner) *Client {
	return &Client{runner: runner}
}

// ghIssueWire is an internal deserialization struct for 'gh issue list --json'.
type ghIssueWire struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
	URL string `json:"url"`
}

// ListIssues queries open issues matching the specified trigger label (INT-3).
func (c *Client) ListIssues(ctx context.Context, repo, label string) ([]IssueDTO, error) {
	args := []string{
		"issue", "list",
		"--repo", repo,
		"--label", label,
		"--state", "open",
		"--limit", "100",
		"--json", "number,title,body,labels,url",
	}

	out, err := c.runner.Run(ctx, nil, args...)
	if err != nil {
		return nil, fmt.Errorf("github: list issues: %w", err)
	}

	var wireIssues []ghIssueWire
	if err := json.Unmarshal(out, &wireIssues); err != nil {
		return nil, fmt.Errorf("github: parse issue list json: %w", err)
	}

	dtos := make([]IssueDTO, len(wireIssues))
	for i, w := range wireIssues {
		labels := make([]string, len(w.Labels))
		for j, l := range w.Labels {
			labels[j] = l.Name
		}
		dtos[i] = IssueDTO{
			Number: w.Number,
			Title:  w.Title,
			Body:   w.Body,
			Labels: labels,
			URL:    w.URL,
		}
	}

	return dtos, nil
}

// EnsureLabel creates or updates a label idempotently using 'gh label create --force' (GHB-2).
func (c *Client) EnsureLabel(ctx context.Context, repo, name, color, description string) error {
	args := []string{
		"label", "create", name,
		"--repo", repo,
		"--color", color,
		"--description", description,
		"--force",
	}

	if _, err := c.runner.Run(ctx, nil, args...); err != nil {
		return fmt.Errorf("github: ensure label %q: %w", name, err)
	}
	return nil
}

// EditLabels adds and/or removes labels on an issue atomically (GHB-2).
func (c *Client) EditLabels(ctx context.Context, repo string, issueNum int, add, remove []string) error {
	if len(add) == 0 && len(remove) == 0 {
		return nil
	}

	args := []string{
		"issue", "edit", strconv.Itoa(issueNum),
		"--repo", repo,
	}
	for _, a := range add {
		args = append(args, "--add-label", a)
	}
	for _, r := range remove {
		args = append(args, "--remove-label", r)
	}

	if _, err := c.runner.Run(ctx, nil, args...); err != nil {
		return fmt.Errorf("github: edit labels for issue #%d: %w", issueNum, err)
	}
	return nil
}

// Comment posts a comment to an issue via stdin pipe (GHB-2, GHB-5).
func (c *Client) Comment(ctx context.Context, repo string, issueNum int, body string) error {
	args := []string{
		"issue", "comment", strconv.Itoa(issueNum),
		"--repo", repo,
		"--body-file", "-",
	}

	if _, err := c.runner.Run(ctx, []byte(body), args...); err != nil {
		return fmt.Errorf("github: comment on issue #%d: %w", issueNum, err)
	}
	return nil
}

// FindPR looks up existing pull requests for a given head branch across all states (DLV-2).
// Returns (nil, nil) if no PR exists for this branch.
func (c *Client) FindPR(ctx context.Context, repo, head string) (*PullRequestDTO, error) {
	args := []string{
		"pr", "list",
		"--repo", repo,
		"--head", head,
		"--state", "all",
		"--json", "number,url,state",
	}

	out, err := c.runner.Run(ctx, nil, args...)
	if err != nil {
		return nil, fmt.Errorf("github: find pr for head %q: %w", head, err)
	}

	var prs []PullRequestDTO
	if err := json.Unmarshal(out, &prs); err != nil {
		return nil, fmt.Errorf("github: parse find pr json: %w", err)
	}

	if len(prs) == 0 {
		return nil, nil
	}
	return &prs[0], nil
}

// prURLRegex extracts trailing PR number from URL (e.g. "https://github.com/org/repo/pull/123").
var prURLRegex = regexp.MustCompile(`/pull/(\d+)`)

// CreatePR creates a new pull request on GitHub with body passed via stdin (DLV-1).
func (c *Client) CreatePR(ctx context.Context, repo, base, head, title, body string) (*PullRequestDTO, error) {
	args := []string{
		"pr", "create",
		"--repo", repo,
		"--base", base,
		"--head", head,
		"--title", title,
		"--body-file", "-",
	}

	out, err := c.runner.Run(ctx, []byte(body), args...)
	if err != nil {
		return nil, fmt.Errorf("github: create pr: %w", err)
	}

	url := strings.TrimSpace(string(out))
	number := 0
	matches := prURLRegex.FindStringSubmatch(url)
	if len(matches) >= 2 {
		num, err := strconv.Atoi(matches[1])
		if err == nil {
			number = num
		}
	}

	// If number could not be extracted from URL, fall back to FindPR
	if number == 0 {
		if pr, findErr := c.FindPR(ctx, repo, head); findErr == nil && pr != nil {
			return pr, nil
		}
	}

	return &PullRequestDTO{
		Number: number,
		URL:    url,
		State:  "OPEN",
	}, nil
}

// CheckVersion executes 'gh --version' to inspect installed version output (CLI-7).
func (c *Client) CheckVersion(ctx context.Context) (string, error) {
	out, err := c.runner.Run(ctx, nil, "--version")
	if err != nil {
		return "", fmt.Errorf("github: check version: %w", err)
	}
	return string(out), nil
}

// CheckAuth executes 'gh auth status' to verify credentials (CLI-7).
func (c *Client) CheckAuth(ctx context.Context) error {
	_, err := c.runner.Run(ctx, nil, "auth", "status")
	if err != nil {
		return fmt.Errorf("github: check auth: %w", err)
	}
	return nil
}
