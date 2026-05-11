---
name: issue-cleanup
license: MIT
metadata:
  version: "1.0"
  # Bump major on breaking prompt/trigger changes; bump minor on new references or fix strategies.
description: >-
  **WORKFLOW SKILL** — Scans the issue backlog for hygiene problems: missing labels,
  missing milestones, customer-reported issues without triage, stale milestone assignments.
  Reports findings grouped by severity and offers interactive fixes.

  INVOKES: gh CLI, GitHub MCP tools, ask_user.

  USE FOR: cleanup check, issue cleanup, backlog cleanup, check issue hygiene,
  backlog scan, find unlabeled issues, find issues without milestones, customer-reported check,
  triage check, orphan issues, stale milestone check.

  DO NOT USE FOR: sprint readiness (use sprint-check), changelog (use changelog-generation),
  code quality (use azd-preflight), hotfix (use hotfix-release).
---

# issue-cleanup

**WORKFLOW SKILL** — Scans the issue backlog for hygiene problems and offers interactive fixes.

INVOKES: `gh` CLI, GitHub MCP tools, `ask_user`.

## Prerequisites

| Tool | Purpose |
|------|---------|
| `gh` | GitHub CLI — authenticated with repo access |

## Preflight

Verify `gh` is authenticated and has access to the repo:

```bash
gh auth status
gh repo view Azure/azure-dev --json name -q .name
```

If project field checks are needed (e.g., sprint-check crossover), also verify `project` scope per sprint-check preflight.

## Workflow

### Step 1 — Determine Scope

Parse the user's request:

| User says | Scope | What to query |
|-----------|-------|---------------|
| "cleanup check" | **Full backlog** | All open issues |
| "cleanup check for May 2026" | **Milestone** | Issues in that milestone |
| "cleanup check for customer-reported" | **Label** | Issues with `customer-reported` label |
| "cleanup check for area/provisioning" | **Area** | Issues with that area label |
| "find issues without milestones" | **Specific** | Open issues with no milestone |
| "find unlabeled issues" | **Specific** | Open issues with no area/* label |

If scope is unclear, ask via `ask_user`:
> What would you like me to scan?

Choices:
- **Full backlog** (all open issues)
- **Customer-reported issues** (Recommended — highest priority)
- **Specific milestone** (I'll ask which one)
- **Specific area label** (I'll ask which one)

### Step 2 — Query Issues

Use `gh` CLI to fetch issues based on scope.

**Full backlog:**
```bash
gh issue list --repo Azure/azure-dev --state open --limit 500 \
  --json number,title,labels,milestone,assignees,createdAt,updatedAt
```

**Customer-reported:**
```bash
gh issue list --repo Azure/azure-dev --state open --label "customer-reported" --limit 200 \
  --json number,title,labels,milestone,assignees,createdAt,updatedAt
```

**Specific milestone:**
```bash
gh issue list --repo Azure/azure-dev --state open --milestone "May 2026" --limit 200 \
  --json number,title,labels,milestone,assignees,createdAt,updatedAt
```

**No milestone:**
```bash
gh issue list --repo Azure/azure-dev --state open --milestone "" --limit 500 \
  --json number,title,labels,milestone,assignees,createdAt,updatedAt
```

For large result sets (500+), paginate and warn the user about volume.

### Step 3 — Analyze Issues

Apply hygiene rules from [references/hygiene-rules.md](references/hygiene-rules.md) (shared with sprint-check).

**Note**: issue-cleanup uses the **full hygiene rules** but applies tier-appropriate checks. An issue in "Future" only needs labels + milestone, while one in "On Deck" needs everything.

For each issue, produce a finding if ANY rule is violated.

### Step 4 — Special: Customer-Reported Triage Check

Customer-reported issues get special treatment:

**🔴 CRITICAL — Customer-reported with no milestone:**
These are customer issues that landed but were never triaged into the planning process.
- Flag as critical
- Offer to set milestone to current month (treat as triage inbox)
- Offer to add `needs-triage` label if not present

**🔴 CRITICAL — Customer-reported with no area label:**
These can't be routed to the right team member.
- Flag as critical
- Present area label list, let user pick

**🟡 WARNING — Customer-reported in past milestone:**
Issue had a milestone assigned but the milestone has passed and issue is still open.
- The work was planned but not completed
- Offer to move to current milestone or On Deck

**Flow for pulling customer-reported into current sprint:**
```
Customer opens issue
  → fabricbot: +customer-reported +question (instant)
  → cleanup-check finds it: "no milestone, no area label"
  → user confirms: set milestone to "May 2026"
  → user picks: area/provisioning
  → team sees it in current milestone during sprint planning
  → team triages: keep in sprint, move to On Deck, or move to Backlog
```

### Step 5 — Report Findings

Group by severity, then by finding type:

```
📋 Issue Cleanup Report — Customer-Reported Issues
═══════════════════════════════════════════════════

🔴 Critical (5 issues — need immediate attention):

  No milestone + no area label:
    #7926 "Feedback after creating Go extension" (created Apr 25)
    #7894 "Product exited" (created Apr 23)

  No milestone (has area label):
    #7507 "Bug: Remote invoke for invocations-protocol..." (created Apr 5)
    #7339 "Azure Extension Crashing" (created Mar 26, area/vscode)
    #7244 "[Issue] AZD should support custom clouds" (created Mar 23)

🟡 Warning (3 issues):

  Past milestone (still open):
    #7564 "agent.yaml not in docs" (April 2026 milestone, due May 6)

  Missing area label:
    #6612 "[Feature request] Add deployment targets" (On Deck)

✅ Clean (12 issues)

Summary: 5 critical, 3 warnings, 12 clean out of 20 issues
```

### Step 6 — Fix Interactively

After the report, offer to fix:

> Found 5 critical issues. Would you like me to fix them?

Choices:
- **Fix all critical issues** (I'll ask for each field choice)
- **Fix one at a time** (I'll go through each issue)
- **Just the report** (no changes)

For each issue being fixed:

1. **Set milestone**: default to current month milestone, confirm via `ask_user`
   ```bash
   gh issue edit NUMBER --repo Azure/azure-dev --milestone "May 2026"
   ```

2. **Add area label**: present area label list, let user pick
   ```bash
   gh issue edit NUMBER --repo Azure/azure-dev --add-label "area/core-cli"
   ```

3. **Add needs-triage**: if not already present
   ```bash
   gh issue edit NUMBER --repo Azure/azure-dev --add-label "needs-triage"
   ```

4. **Assign**: offer to assign to the user or someone else
   ```bash
   gh issue edit NUMBER --repo Azure/azure-dev --add-assignee "rajeshkamal5050"
   ```

### Step 7 — Stale Milestone Check

Find issues in past milestones that are still open:

```bash
# Get all milestones with due dates
gh api repos/Azure/azure-dev/milestones --jq '.[] | select(.due_on != null and .open_issues > 0) | "\(.number) \(.title) \(.due_on) \(.open_issues)"'
```

For each past-due milestone with open issues:
- List the issues
- Offer to move them to current milestone, On Deck, or Backlog

---

## Error Handling

- `gh` not authenticated → stop, tell user to run `gh auth login`
- Too many issues (>500) → warn, suggest narrowing scope
- Rate limiting → wait and retry with backoff
- Issue edit fails → log error, continue with next issue
- Milestone not found → list available milestones, let user pick
