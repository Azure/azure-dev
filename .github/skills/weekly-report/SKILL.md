---
name: weekly-report
license: MIT
metadata:
  version: "1.0"
description: >-
  Generate weekly executive reports for the azd team. Pulls PR/issue/release
  data from GitHub, reads changelogs, combines with team notes, and produces
  a polished markdown report.

  INVOKES: gh CLI, jq, git CLI, explore sub-agents, ask_user.

  USE FOR: weekly report, exec report, status update, team update, weekly
  summary, generate report, report time.
  DO NOT USE FOR: demo videos (use weekly-demo-video), release changelogs
  (use changelog-generation), general docs, PRs.
---

# Weekly Executive Report Generator

Generates weekly executive reports for the Azure Developer CLI (azd) team leadership.

## Prerequisites

| Tool | Purpose |
|------|---------|
| `gh` | GitHub CLI — authenticated with repo access |
| `jq` | JSON filtering for release queries |
| `git` | Repository data |

## Report Format (strict order)

```
📝 TLDR
📊 Metrics
⚠️ Risks + Blockers
🎯 Changelog
📋 This Week
🔜 Next Week
🚩 Learnings
🔥 Demos/Links
```

## Execution Flow

### Step 1: Confirm date range

Cadence is **Thursday to Thursday**. Confirm with the user if unclear.

```
# Example: Thursday May 1 to Thursday May 8, 2026
```

### Step 2: Pull repo data

```bash
cd <repo-root>  # the azure-dev repository root
git checkout main && git pull --rebase
```

**PRs merged:**
```bash
gh pr list --repo Azure/azure-dev --state merged --search "merged:2026-05-01..2026-05-08" --limit 200 --json number,title,mergedAt
```

**Issues closed:**
```bash
gh issue list --repo Azure/azure-dev --state closed --search "closed:2026-05-01..2026-05-08" --limit 200 --json number,title,closedAt
```

**Releases:**
```bash
gh api repos/Azure/azure-dev/releases --paginate | jq '[.[] | select(.published_at >= "2026-05-01" and .published_at <= "2026-05-08") | {name, tag_name, published_at}]'
```

### Step 3: Read changelogs

Changelog data is maintained by the `changelog-generation` skill.

- Core: `cli/azd/CHANGELOG.md`
- Agents: `cli/azd/extensions/azure.ai.agents/CHANGELOG.md`
- Finetuning: `cli/azd/extensions/azure.ai.models/CHANGELOG.md`

### Step 4: Wait for team notes

Do NOT assemble the report until the user provides team notes from emails, Teams, meetings, etc.
Only product and engineering content.

### Step 5: Assemble report

Combine repo data with team notes. Follow all rules below.

### Step 6: Iterate

Expect 5–15 rounds of edits. Apply changes surgically. After removals, check for related content elsewhere (risks → next week → learnings → TLDR) and offer to clean up.

## Output

Save reports to the Copilot CLI session workspace (`~/.copilot/session-state/<session-id>/files/`) as `weekly-report-{month}{startday}-{endday}.md`.

## Section Rules

{{ references/SECTION-RULES.md }}

## Tone and Style

{{ references/TONE-RULES.md }}

## Example Report

Use the latest completed report in the session files as a format reference.
