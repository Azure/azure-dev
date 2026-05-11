---
name: sprint-check
license: MIT
metadata:
  version: "1.0"
  # Bump major on breaking prompt/trigger changes; bump minor on new references or fix strategies.
description: >-
  **WORKFLOW SKILL** — Validates sprint readiness for issues and PRs.
  Checks labels, milestones, project fields (Sprint, Priority, Initiative),
  EPIC parents, and assignments. Can fix fields interactively with user confirmation.

  INVOKES: gh CLI, GitHub MCP tools, ask_user.

  USE FOR: sprint check, sprint-check, check sprint readiness, check my sprint,
  sprint check for the team, sprint planning, mid-sprint check, sprint close check,
  prepare issue for sprint, make issue sprint ready, pull issue into sprint.

  DO NOT USE FOR: backlog cleanup (use issue-cleanup), changelog (use changelog-generation),
  code quality checks (use azd-preflight), release (use hotfix-release).
---

# sprint-check

**WORKFLOW SKILL** — Validates sprint readiness for issues and PRs in the current sprint.

INVOKES: `gh` CLI, GitHub MCP tools, `ask_user`.

## Prerequisites

| Tool | Purpose |
|------|---------|
| `gh` | GitHub CLI — authenticated with `project` scope |

## Preflight

Before any operation, verify access:

```bash
gh auth status
```

**Check for `project` scope.** If missing, tell the user:
> Your `gh` token needs the `project` scope to read/write project fields.
> Run: `gh auth refresh --scopes project`

Then verify project access:
```bash
gh api graphql -f query='{
  organization(login: "Azure") {
    projectV2(number: 182) { title }
  }
}'
```

If this fails, stop and report the error.

## Workflow

### Step 1 — Determine Scope

Parse the user's request to determine scope:

| User says | Scope | What to query |
|-----------|-------|---------------|
| "sprint-check" / "check my sprint" | **Personal** | Issues assigned to the current `gh` user in the current sprint |
| "sprint-check for the team" | **Team** | All issues in the current sprint |
| "sprint-check for @username" | **User** | Issues assigned to `@username` in the current sprint |
| "check issue #1234 for sprint" | **Single issue** | Validate one issue for sprint readiness |
| "pull #1234 into this sprint" | **Single issue + fix** | Validate + fix all fields |

If ambiguous, ask via `ask_user`.

### Step 2 — Get Current Sprint

Query the current sprint iteration from Project #182:

```bash
gh api graphql -f query='{
  organization(login: "Azure") {
    projectV2(number: 182) {
      field(name: "Sprint") {
        ... on ProjectV2IterationField {
          id
          configuration {
            iterations { id title startDate duration }
          }
        }
      }
    }
  }
}'
```

Find the iteration where `today >= startDate && today < startDate + duration`.
Store the sprint `id`, `title`, and the field `id` for later mutations.

### Step 3 — Query Sprint Issues

**For team/personal scope** — query all project items in the current sprint:

```bash
gh api graphql --paginate -f query='
query($cursor: String) {
  organization(login: "Azure") {
    projectV2(number: 182) {
      items(first: 100, after: $cursor) {
        pageInfo { hasNextPage endCursor }
        nodes {
          content {
            ... on Issue {
              number
              title
              state
              assignees(first: 10) { nodes { login } }
              labels(first: 20) { nodes { name } }
              milestone { title }
              parent { number title }
            }
          }
          fieldValueByName(name: "Sprint") {
            ... on ProjectV2ItemFieldIterationValue { title iterationId }
          }
          fieldValueByName(name: "Priority") {
            ... on ProjectV2ItemFieldSingleSelectValue { name optionId }
          }
          fieldValueByName(name: "Initiative") {
            ... on ProjectV2ItemFieldSingleSelectValue { name optionId }
          }
        }
      }
    }
  }
}'
```

Filter items where Sprint title matches the current sprint.
For personal scope, further filter by assignee matching the `gh` user.

**For single-issue scope** — query the specific issue:

```bash
gh api graphql -f query='{
  repository(owner: "Azure", name: "azure-dev") {
    issue(number: ISSUE_NUMBER) {
      number title state
      assignees(first: 10) { nodes { login } }
      labels(first: 20) { nodes { name } }
      milestone { title number }
      parent { number title }
      projectItems(first: 10) {
        nodes {
          project { number }
          id
          fieldValueByName(name: "Sprint") {
            ... on ProjectV2ItemFieldIterationValue { title iterationId }
          }
          fieldValueByName(name: "Priority") {
            ... on ProjectV2ItemFieldSingleSelectValue { name optionId }
          }
          fieldValueByName(name: "Initiative") {
            ... on ProjectV2ItemFieldSingleSelectValue { name optionId }
          }
        }
      }
    }
  }
}'
```

### Step 4 — Validate Each Issue

Apply the hygiene rules from [references/hygiene-rules.md](references/hygiene-rules.md).

For each issue, check:

{{ references/hygiene-rules.md }}

### Step 5 — Report Findings

Group findings by severity:

**🔴 Critical** (blocks sprint work):
- No assignee
- No milestone
- No area/* label

**🟡 Warning** (should fix):
- No Priority set (project field)
- No Initiative set (project field)
- Missing EPIC parent (if initiative is quarterly, not 🛡️ Ongoing)
- Milestone doesn't match current month
- Customer-reported without `needs-triage` removed (still pending triage)

**🟢 Info**:
- Multiple area labels (fine, just note it)
- Has `needs-team-attention` (expected for customer-reported)

Present as a table:

```
Sprint: May 2026 - Sprint 2 (May 5 – May 16)

🔴 Critical (3 issues):
  #7926 "Feedback after creating Go extension" — no assignee, no milestone, no area label
  #7894 "Product exited" — no assignee, no milestone, no area label
  #8033 "Create skills..." — no labels, no milestone

🟡 Warning (2 issues):
  #7248 "Handle DeploymentActive..." — no Priority, no Initiative
  #7712 "Emergency hotfix release..." — no Initiative

✅ Clean (4 issues):
  #8026, #7680, #7317, #435

Summary: 3 critical, 2 warnings, 4 clean out of 9 issues
```

### Step 6 — Fix Interactively

After reporting, offer to fix issues:

> Found 3 issues with critical problems. Would you like me to fix them?

For each fixable field, apply the rules:

**Deterministic fixes** (apply with confirmation):
- **Add to project**: if issue not in project #182, add it first via `addProjectV2ItemById`
- **Set Sprint**: set to current sprint iteration
- **Set Milestone**: set to current month milestone
- **Assign**: assign to the user running the skill (or ask who)

**Require user choice** (present options via `ask_user`):
- **Area label**: present the list of `area/*` labels, let user pick
- **Priority**: present Priority options from the project field
- **Initiative**: present Initiative options from the project field

For project field mutations, see [references/project-mutations.md](references/project-mutations.md).

For milestone/label fixes via `gh` CLI:

```bash
# Set milestone
gh issue edit ISSUE_NUMBER --repo Azure/azure-dev --milestone "May 2026"

# Add label
gh issue edit ISSUE_NUMBER --repo Azure/azure-dev --add-label "area/core-cli"

# Assign
gh issue edit ISSUE_NUMBER --repo Azure/azure-dev --add-assignee "@me"
```

### Step 7 — Pull Issue Into Sprint (single-issue mode)

When the user says "pull #1234 into this sprint" or "make #1234 sprint ready":

1. Query the issue (Step 3, single-issue)
2. Validate (Step 4)
3. For each missing field, ask the user what value to set
4. Apply all fixes
5. Confirm: "Issue #1234 is now sprint-ready ✅"

This is the "given an issue being pulled into this sprint, make it ready" flow.

### Step 8 — Sprint Close / Rollover

**Trigger**: This step activates when:
- The user says "sprint close check" or similar
- OR today is within 1 day of the current sprint's end date (auto-detected)

**Workflow**:

1. **Detect sprint end proximity** — from Step 2, calculate `sprint_end = startDate + duration`. If `today >= sprint_end - 1 day`, this step activates automatically after the normal report.

2. **Identify the next sprint** — from the iterations list (Step 2), find the iteration whose `startDate` is immediately after the current sprint's end date. Store its `id` and `title`.

3. **Classify open issues** — for each open issue still in the current sprint, present:

```
Sprint closing: May 04 - May 10 → Next: May 11 - May 17

Open issues to resolve:
  #8033 — Create skills for weekly reports...        → Move to May 11-17? [Y/n]
  #8026 — RBAC propagation race causes 403...        → Move to May 11-17? [Y/n]
  #7712 — Emergency hotfix release process...        → Move to May 11-17? [Y/n]
```

4. **Ask user for each issue** via `ask_user`:
   - **Move to next sprint** (default) — update Sprint field to next iteration
   - **Keep in current sprint** — leave as-is (will show as overdue)
   - **Remove from sprint** — clear Sprint field (back to backlog)

5. **Apply moves** — for issues the user confirms, update the Sprint iteration field:

```bash
gh api graphql -f query='
mutation {
  updateProjectV2ItemFieldValue(input: {
    projectId: "PROJECT_ID"
    itemId: "ITEM_ID"
    fieldId: "SPRINT_FIELD_ID"
    value: { iterationId: "NEXT_SPRINT_ITERATION_ID" }
  }) {
    projectV2Item { id }
  }
}'
```

See [references/project-mutations.md](references/project-mutations.md) for full mutation details.

6. **Summary** — after all moves:

```
Sprint rollover complete:
  ✅ 3 issues moved to May 11 - May 17
  ⏸️ 1 issue kept in May 04 - May 10
  📋 0 issues removed from sprint
```

---

## Error Handling

- `gh` not authenticated → stop, tell user to run `gh auth login`
- Project access denied → stop, explain `project` scope requirement
- Issue not in project → offer to add it via `addProjectV2ItemById`
- Sprint field not found → warn, skip sprint-related checks
- Rate limiting → wait and retry with backoff
- Issue is closed → skip, note in report
