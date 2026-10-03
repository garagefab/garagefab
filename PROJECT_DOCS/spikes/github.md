# Spike B: GitHub Research

> Date: 2026-10-03
> Status: **Draft — documentation-only.** No experiment was run against a real repository or Project. Every finding is tagged `[docs]` (from GitHub documentation or public knowledge) and `[unverified]` until an experiment confirms it. Replace each tag with `[verified: <date>]` plus the recorded request and response.

## Summary Table

| # | Question | Findings |
|---|----------|----------|
| 1 | Read Projects v2 | GraphQL only. `items(first:100, after:$cursor)` with `fieldValues` on `ProjectV2ItemFieldSingleSelectValue` returns `Status` and `Type`. Cursor pagination via `pageInfo`. Cost about 1 point per page of 100 items. `[docs][unverified]` |
| 2 | Update Status | Mutation `updateProjectV2ItemFieldValue` with `projectId`, `itemId`, `fieldId`, `value.singleSelectOptionId`. Field and option IDs must be queried first and cached. `[docs][unverified]` |
| 3 | Token permissions | Classic: `project` (read: `read:project`) and `repo`. Fine-grained: Issues, Pull requests, Metadata, plus organization Projects. Fine-grained tokens cannot reach user-owned Projects. `[docs][unverified]` |
| 4 | Create PR, detect existing | `POST /repos/{o}/{r}/pulls`. A second create for the same head returns `422` with "A pull request already exists". Detect with `GET /repos/{o}/{r}/pulls?head={owner}:{branch}&state=open`. `[docs][unverified]` |
| 5 | Rate limits | 30 s poll, 5 projects: about 600 GraphQL points/hour against a 5,000/hour budget. A 30 s default is safe. Conditional requests do not help GraphQL. `[docs][unverified]` |
| 6 | `Closes #n` | The closing keyword closes the issue only when the PR merges into the default branch. Moving the item to Done needs the Project's "Item closed" built-in workflow, mapped to the `07_Done` option. `[docs][unverified]` |

## Detailed Findings

### Q1 — Read Projects v2 Items

Projects v2 has no REST API. All reads use `POST https://api.github.com/graphql`.

Resolve the project first. The owner type decides the root field:

```graphql
query($login: String!, $number: Int!) {
  user(login: $login)         { projectV2(number: $number) { id title } }
}
# organization:
query($login: String!, $number: Int!) {
  organization(login: $login) { projectV2(number: $number) { id title } }
}
```

Read items with their single-select values:

```graphql
query($projectId: ID!, $cursor: String) {
  rateLimit { cost remaining resetAt }
  node(id: $projectId) {
    ... on ProjectV2 {
      items(first: 100, after: $cursor) {
        pageInfo { hasNextPage endCursor }
        nodes {
          id
          status: fieldValueByName(name: "Status") {
            ... on ProjectV2ItemFieldSingleSelectValue { name optionId }
          }
          type: fieldValueByName(name: "Type") {
            ... on ProjectV2ItemFieldSingleSelectValue { name optionId }
          }
          content {
            __typename
            ... on Issue { number title body url repository { nameWithOwner } }
          }
        }
      }
    }
  }
}
```

Conclusions:

- Item `content` can be `Issue`, `PullRequest`, or `DraftIssue`. The provider must skip non-`Issue` items and record an intake error for them (INT-3).
- Items with no `Status` or `Type` value return `null`, not an error. A missing `Type` must produce the visible intake error required by INT-3.
- Use `fieldValueByName` instead of `fieldValues(first:N)`: it avoids a nested connection and keeps cost low.
- Paginate until `hasNextPage` is false. Stop early is not possible: `items` has no documented server-side `Status` filter, so the poller must read all items and filter locally. `[unverified]`: a `query` argument on `items` may exist; test it.

**To record:** real response JSON, `rateLimit.cost` for one page, behavior at more than 100 items.

### Q2 — Update Item Status

Look up the field and option IDs once per project, then cache them in memory:

```graphql
query($projectId: ID!) {
  node(id: $projectId) {
    ... on ProjectV2 {
      field(name: "Status") {
        ... on ProjectV2SingleSelectField { id options { id name } }
      }
    }
  }
}
```

Update:

```graphql
mutation($projectId: ID!, $itemId: ID!, $fieldId: ID!, $optionId: String!) {
  updateProjectV2ItemFieldValue(input: {
    projectId: $projectId, itemId: $itemId, fieldId: $fieldId,
    value: { singleSelectOptionId: $optionId }
  }) { projectV2Item { id } }
}
```

Conclusions:

- The mirror must map stage names (`01_Intent` … `07_Done`) to option IDs by **name**. A missing option is a configuration error shown on the Overview page, not a crash.
- Option IDs change if a user edits the field options. Refresh the cache on a "not found" error.

**To record:** the exact mutation response and the error returned for a bad option ID.

### Q3 — Token Permissions

| Operation | Classic PAT | Fine-grained PAT |
|-----------|-------------|------------------|
| Read Project items (user-owned) | `read:project` or `project` | **Not supported** `[docs]` |
| Read Project items (org-owned) | `read:project` or `project` | Organization permission "Projects": read |
| Update Project item (org-owned) | `project` | Organization permission "Projects": read and write |
| Read issue content | `repo` (private) or none (public) | Repository permission "Issues": read |
| Create PR / list PRs | `repo` (or `public_repo` for public) | Repository permission "Pull requests": read and write |
| All requests | — | "Metadata": read (granted automatically) |

Conclusions:

- This matches `architecture.md` §12.3. The permission names above use GitHub's UI wording.
- Fine-grained tokens must have the **resource owner** set to the organization, and the organization may require owner approval for the token. Document this.
- Pushing uses the user's git credentials, so the token needs no "Contents" permission. `[unverified]`: confirm that PR creation succeeds without "Contents" on a fine-grained token when the head branch is in the same repository.

**To record:** a 4-row matrix (classic/fine-grained × personal/organization) with pass or fail for each of Q1, Q2, Q4.

### Q4 — Create PR and Detect Existing

Create:

```http
POST /repos/{owner}/{repo}/pulls
{ "title": "...", "head": "garagefab/job-178", "base": "main", "body": "...\n\nCloses #178", "draft": false }
```

Second create for the same head and base: `422 Unprocessable Entity`, body contains `errors[].message` starting with "A pull request already exists for". Do not parse the message to decide. Call the list endpoint first:

```http
GET /repos/{owner}/{repo}/pulls?head={owner}:{branch}&state=open
```

Conclusions:

- `head` must be `owner:branch` in the list call, or the filter is ignored and all open PRs return. This is easy to get wrong.
- Delivery must be idempotent (RCV): list first, reuse the existing PR number if present, create otherwise. Treat a `422` on create as a race and re-list once.
- A closed or merged PR for the same branch does not block a new create. Use `state=open`.

**To record:** the exact 422 body and the list response for an existing PR.

### Q5 — Rate-Limit Behavior

GraphQL cost is `ceil(total connection requests / 100)`. One page of 100 items with `fieldValueByName` and a `content` object: the `items` connection counts 1 request, so the cost is about 1 point. `[docs][unverified]`

| Scenario | Polls/hour | Points/hour | Share of 5,000 |
|----------|-----------|-------------|----------------|
| 1 project, 30 s | 120 | ~120 | 2.4% |
| 5 projects, 30 s | 600 | ~600 | 12% |
| 5 projects, 30 s, 3 pages each | 1,800 | ~1,800 | 36% |

Conclusions:

- The 30 s default is safe for a handful of projects with fewer than 100 items each.
- Conditional requests (`ETag`, `If-None-Match`) work for REST only. GraphQL uses `POST`, so they give no saving. Do not build them for the Projects poll.
- Read `rateLimit { remaining resetAt }` on each poll. When `remaining` falls below 10%, double the interval until `resetAt`.
- Secondary rate limits (abuse limits) also apply: honor `Retry-After` and `403`/`429` responses with back-off. `[docs]`
- The mirror (writes) costs 1 mutation per stage change. This is small, but mutations count toward the secondary limit. Serialize them.

**To record:** the measured `cost` and `remaining` from real polls.

### Q6 — `Closes #n` with Project Workflows

- A closing keyword (`Closes #178`) closes the issue only when the PR is merged into the repository's **default branch**. A PR into another branch does not close it. `[docs]`
- Closing the issue triggers the Project's built-in workflow "Item closed", which sets `Status`. New Projects enable it by default and target the option named `Done`. `[docs][unverified]`
- Garagefab's board uses custom names (`07_Done`). The built-in workflow must be edited to set `07_Done`, or the mirror will have to set it. Decision: the mirror sets `07_Done` itself after merge detection, and the docs tell users to disable or retarget the built-in workflow to avoid two writers.
- `pr_issue_keyword: refs` (OQ-14) writes `Refs #n`, which links without closing. The issue then stays open, so `Status` does not move on its own.

**To record:** merge a test PR with `Closes #n`, then read the item's `Status` before and after.

## Provider Design Implications

1. One GraphQL client with `rateLimit` inspection and back-off; one REST client for PRs.
2. Poller reads all items per project each cycle, filters locally by `Status=01_Intent`, and requires a `Type` value.
3. Cache project ID, `Status` field ID, and option IDs per project; refresh on error.
4. Delivery: list open PRs for `owner:branch` first, create only if empty, re-list on `422`.
5. Handle `DraftIssue` and `PullRequest` item content explicitly.
6. Do not implement ETag support for the Projects poll.

## Decisions to Update

| Document | Change | Reason |
|----------|--------|--------|
| `architecture.md` §12.3 | Keep as is until experiments confirm the permission names; remove the sentence "Exact fine-grained permission names are verified … when the provider is implemented" once Q3 is verified | Q3 |
| `architecture.md` §12.1 | Add: poller filters `Status` locally; no ETag for GraphQL | Q1, Q5 |
| `architecture.md` §12.2 | Add: delivery lists open PRs for `owner:branch` before create | Q4 |
| `architecture.md` §3 Decision Log | New entry: mirror owns `07_Done`; document disabling the built-in "Item closed" workflow or retargeting it | Q6 |
| `spec.md` OQ-1, OQ-14 | Mark "Confirmed" only after Q1 and Q6 are verified | Q1, Q6 |

No document change is applied yet. All of them wait for verified results.

## Open Issues

- All six questions need real experiments. Required: a test repository, a user-owned Project, an organization-owned Project, and the four token types from Q3.
- Does `ProjectV2.items` accept a server-side filter (`query`)? If yes, the poller can read fewer items.
- Does a fine-grained token with only "Pull requests: write" create a PR from a same-repo branch without "Contents: read"?
- Do fine-grained tokens now support user-owned Projects? Confirm against the current documentation.
- Which option name does the built-in "Item closed" workflow target on a Project with custom Status options?
