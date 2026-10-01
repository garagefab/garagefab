# SB — Spike B: GitHub

> Status: **Not started**
> Size: S · Depends on: —
> Unblocks: M6, `architecture.md` §12, `spec.md` OQ-1 and OQ-14

## Goal

Verify the GitHub assumptions before building the provider (`architecture.md` §12, `spec.md` OQ-1). Answer the 6 questions to investigate GitHub Projects and REST APIs.

## Deliverables

| Artifact | Location | Purpose |
|----------|----------|---------|
| Spike report | `docs/spikes/github.md` | Answers all 6 questions with evidence |
| Decision updates | `PROJECT_DOCS/02_architecture.md` §3 | New entries if any assumption changes |
| Open question closures | `architecture.md` §12, `spec.md` OQ-1, OQ-14 | Mark resolved with references to findings |

## Method

This is a **research spike**, not a coding task. The work is:
1. Read GitHub REST and GraphQL API documentation.
2. Run controlled experiments (e.g., using `curl`, `gh api`, or a simple Go script).
3. Record queries, responses, and observations.
4. Write findings into the spike report.

## Questions and Experiment Plan

### Q1 — Read Projects v2 Items
**Question:** Read Projects v2 items with their `Status` and `Type` single-select fields through GraphQL; measure query cost and pagination.
**Experiments:** Write a GraphQL query to fetch items from a user or org Project v2. Ensure `Status` and `Type` field values are retrieved. Test pagination.
**Record:** GraphQL query, sample JSON response, query cost reported by GitHub API, pagination details.

### Q2 — Update Item Status
**Question:** Update an item's `Status` through GraphQL.
**Experiments:** Write a GraphQL mutation to update the `Status` field of a Project v2 item.
**Record:** GraphQL mutation, variables used, sample response.

### Q3 — Verify Token Permissions
**Question:** Verify required token permissions for each operation, for classic and fine-grained tokens, on a personal account and on an organization (confirms `architecture.md` §12.3 and exact fine-grained permission names).
**Experiments:** Test the queries from Q1 and Q2, as well as PR creation (Q4) using:
- Classic PAT (personal account)
- Fine-grained PAT (personal account)
- Classic PAT (organization)
- Fine-grained PAT (organization)
**Record:** Which operations succeed/fail for each token type and account type. The exact permission scopes required.

### Q4 — Create PR and Detect Existing
**Question:** Create a pull request via REST; detect an existing PR for a branch.
**Experiments:** Use the GitHub REST API to create a PR. Try to create a second PR for the same branch and observe the error. Write a query to find an existing PR for a specific branch.
**Record:** REST API endpoints used, payload structure, error response when PR exists, query to find existing PR.

### Q5 — Rate Limit Behavior
**Question:** Rate-limit behavior of a 30-second poll with several projects; whether a longer interval or conditional requests are needed.
**Experiments:** Calculate the cost of the polling queries (from Q1) running every 30 seconds for e.g. 5 projects. Compare against GitHub API rate limits.
**Record:** Rate limit consumption calculations, recommendations for conditional requests (ETag / Last-Modified) or polling intervals.

### Q6 — Closes #n Behavior
**Question:** Behavior of `Closes #n` with Project built-in workflows.
**Experiments:** Create a PR with `Closes #<issue_number>` in the body. Check if merging the PR automatically moves the linked issue to the "Done" column in the Project v2 (if workflows are configured).
**Record:** Observations on the automated status changes in Projects v2.

## Output Structure

The spike report (`docs/spikes/github.md`) should follow this template:

```markdown
# Spike B: GitHub Research

> Date: YYYY-MM-DD

## Summary Table

| # | Question | Findings |
|---|----------|----------|
| 1 | Read Projects v2 | ... |
| ... | ... | ... |

## Detailed Findings

### Q1 — Read Projects v2 Items
[queries, responses, conclusions]

...

## Provider Design Implications
[What this means for the GitHub provider implementation in M6]

## Decisions to Update
[Changes needed in architecture.md and spec.md]

## Open Issues
[Anything unresolved that needs follow-up]
```

## Exit Criteria
- [ ] All 6 questions answered with recorded evidence.
- [ ] `docs/spikes/github.md` is complete.
- [ ] `architecture.md` §12 verified or updated.
- [ ] `spec.md` OQ-1 and OQ-14 answered.
- [ ] Any assumption changes recorded as decision log entries.
