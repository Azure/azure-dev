---
name: Update Instructions From PR Reviews
on:
  schedule:
    # NOTE: Using the fuzzy form for scheduling lets gh-aw jitter the exact minute
    # across repos so scheduled runs don't all spike at the same time.
    - cron: "weekly on monday"
  workflow_dispatch:
    inputs:
      since:
        description: >-
          Mine PRs merged on/after this date (YYYY-MM-DD). Default: last
          successful run date, or PRs merged within the last 6 months on the
          first run.
        required: false
        type: string
      max_prs:
        description: "Max merged PRs to examine. Default: 50."
        required: false
        default: "50"
        type: string
      repository:
        description: >-
          Repo to mine PRs from, as `owner/repo`. Default: this repo. A private
          source repo needs read access via COPILOT_GITHUB_TOKEN.
        required: false
        type: string
      min_pr_count:
        description: >-
          Promote a theme only if it spans at least this many distinct PRs
          (with min_reviewer_count). Default: 2.
        required: false
        default: "2"
        type: string
      min_reviewer_count:
        description: >-
          Promote a theme only if at least this many distinct reviewers raised
          it (with min_pr_count). Default: 2.
        required: false
        default: "2"
        type: string
      min_single_reviewer_count:
        description: >-
          Also promote a theme if one reviewer flags it at least this many
          times. Default: 3.
        required: false
        default: "3"
        type: string

# The agent only needs to read the repo and its PR history. All writes happen
# through the create-pull-request safe output in a separate, permission-scoped job.
permissions:
  contents: read
  pull-requests: read
  issues: read

engine: copilot

tools:
  # Native GitHub MCP toolsets — no gh/jq/git needed. These let the agent list
  # merged PRs and read their reviews, inline comments, and conversation comments.
  github:
    toolsets: [pull_requests, issues, repos]
  # Lets the agent read and edit the local .github/ customization files.
  edit:
  # Remembers the last successful run date so "since last run" works with no
  # external state. The agent reads/writes a small marker here each run.
  cache-memory:

safe-outputs:
  create-pull-request:
    title-prefix: "[auto] "
    labels: [automation, copilot-instructions]
    draft: false
    # Open a fresh PR only when there isn't already an open one; otherwise reuse
    # the same branch so the existing (unmerged) PR is updated in place.
    preserve-branch-name: true
    recreate-ref: true
    # If there were no new themes worth promoting, do nothing quietly.
    if-no-changes: "ignore"
    # Some orgs block "GitHub Actions creating/approving PRs" (Settings → Actions
    # → General → Workflow permissions). When that policy is off, the safe-output
    # job can't open the PR directly. Rather than fail (or require a PAT), fall
    # back to opening an ISSUE with a link to the pushed branch — and assign it to
    # `copilot` so the coding agent picks it up and opens the PR for us. This is
    # the simplest no-secret path; gh-aw grants the job `issues: write` for the
    # fallback automatically. See: https://github.github.com/gh-aw/reference/faq/
    fallback-as-issue: true
    assignees: [copilot]
    #
    # ── Prefer real PRs? Pick ONE of these and remove the fallback above ──────
    # The issue fallback above needs no secrets, but if you'd rather the workflow
    # always open a proper pull request, give the safe-output job an identity that
    # ISN'T subject to the "Actions can't create PRs" policy:
    #
    #   A) Flip the org/repo policy (no config needed): Settings → Actions →
    #      General → Workflow permissions → "Allow GitHub Actions to create and
    #      approve pull requests". Then you can drop the fallback entirely.
    #
    #   B) Author the PR with a PAT (contents:write + pull-requests:write),
    #      stored as a secret — the policy only applies to the default token:
    # github-token: ${{ secrets.AW_PR_TOKEN }}
    #
    #   C) Author the PR via a GitHub App install token (cleanest for orgs —
    #      no personal seat, scoped install):
    # github-app:
    #   app-id: ${{ vars.AW_APP_ID }}
    #   private-key: ${{ secrets.AW_APP_PRIVATE_KEY }}
    #
    # This workflow is explicitly designed to manage instruction files under
    # .github/ (normally protected). Restrict it to ONLY those files and allow
    # them through protection. Any patch touching anything else is refused.
    allowed-files:
      - .github/copilot-instructions.md
      - .github/instructions/**
      - .github/prompts/**
      - .github/agents/**
      - AGENTS.md
    protected-files: allowed
---

# Update Instructions From PR Reviews

You mine this repository's recently-merged pull request review feedback and turn
recurring reviewer asks into additive edits to the repo's Copilot/agent
customization files under `.github/`, then open (or update) a single pull request
with the proposed changes. The goal: future PRs get the same feedback
automatically, both from developers using Copilot locally and from Copilot Code
Review acting as a backstop.

Work entirely with the **GitHub tools** (to read PRs and comments) and the
**edit tool** (to read and write the local `.github/` files). Do not shell out to
`gh`, `jq`, or `git` — the GitHub tools provide everything you need to read, and
the create-pull-request safe output handles all writing.

## Run parameters

These are the **resolved values from this run's trigger**. Treat them as the
source of truth — do not re-derive them from the prose defaults below. If
**Since** reads `not set`, no explicit date was supplied; resolve it using the
fallback chain in step 1 below.

- Source repository: `${{ github.event.inputs.repository || github.repository }}`
- Since: `${{ github.event.inputs.since || 'not set' }}`
- Max PRs: `${{ github.event.inputs.max_prs || '50' }}`
- Min PR count: `${{ github.event.inputs.min_pr_count || '2' }}`
- Min reviewer count: `${{ github.event.inputs.min_reviewer_count || '2' }}`
- Min single-reviewer count: `${{ github.event.inputs.min_single_reviewer_count || '3' }}`

## 1. Determine the PR range

Decide which merged PRs to examine, in this order of preference:

1. If the **Since** run parameter above is a real date (not `not set`), mine
   PRs merged on/after that date.
2. Otherwise, read the **cache memory** for a marker file named
   `last-run.txt`. If it exists, mine PRs merged after that timestamp ("since
   last run").
3. Otherwise (first ever run, no marker), mine PRs merged within the last 6
   months, capped at the **Max PRs** run parameter.

The **Source repository** run parameter above already resolves to the repository
this workflow runs in when no override was supplied, so mine PRs from whatever
`owner/repo` it shows.

Note that the source repo (where PRs are mined) and the target repo (whose
`.github/` files you edit and open a PR against) are independent. You always
edit and open the PR against the **repository this workflow runs in**, even when
mining a different source. Report both the resolved source repo and the
effective range before fetching.

## 2. Fetch PR review feedback

Using the GitHub tools, list the in-scope **merged** PRs and, for each, collect
all three review signals:

- **Inline review comments** — line-level comments on the diff.
- **Review summary bodies** — the top-level body of each submitted review.
- **PR conversation comments** — issue-style comments on the PR thread.

Report the PR count and a sample of titles.

## 3. Filter low-signal noise

Keep only genuine **reviewer asks**. Drop:

- Bot/automation accounts (logins ending in `[bot]`, plus known automation like
  `copilot-pull-request-reviewer`, `copilot-swe-agent`, `azure-sdk`).
- `LGTM` / `+1` / emoji-only / very short (< ~30 char) comments.
- Quoted-only replies that add no new feedback.
- **Author replies and status updates** — "Fixed in abc123", "Intentional",
  "Not taken", "Agreed", "Good catch, done". These are acknowledgements, not
  asks; do not count them toward the promotion threshold. If such a reply
  clarifies a real reviewer concern, trace the rule back to the original ask.

Report the kept-vs-dropped ratio.

## 4. Cluster into themes

Group the surviving reviewer asks by recurring topic. Useful axes: file/path
patterns (e.g. `*_test.go`, `bicep/**`), recurring phrases ("error wrap", "nil
check", "context cancellation", "secret", "logging", "accessibility"), and
reviewer (does someone flag the same class of issue repeatedly?).

Produce a themes table with, for each theme: a representative example, the source
PR numbers, the count, and a suggested home file.

**Promotion threshold** — only promote a theme to a rule when it appears in **≥
Min PR count PRs from ≥ Min reviewer count distinct reviewers**, or when a
single reviewer flags it **≥ Min single-reviewer count times**. Use the values
from the **Run parameters** section above; when one is empty, default to **2**,
**2**, and **3** respectively. Discard everything below the threshold.

## 5. Inventory existing customizations

Look at the `.github/` folder on the main branch of the **repository this
workflow runs in** (the target repo, not the source repo you mined PRs from) to
figure out where and how to update the Copilot/agent instructions. That target
repo's main branch is the base for your subsequent pull request.

## 6. Propose and apply edits

For each promoted theme:

1. **Already covered?** Strengthen the existing wording rather than duplicating.
2. **Has an obvious scoped home?** (e.g. Go-only → `.github/instructions/go.instructions.md`
   with `applyTo: '**/*.go'`) Edit that file.
3. **Otherwise** add it to `.github/copilot-instructions.md` under a clearly
   labeled section.

Rules for the edits:

- Prefer **minimal, additive** changes. Never rewrite existing instructions
  wholesale.
- **Cite source PRs** in a trailing italic line on every rule you add, so future
  maintainers can audit *why* it exists (e.g. `_Source: #7012, #7034_`).
- Keep language-specific rules in scoped `*.instructions.md` files, not in the
  always-on `copilot-instructions.md`.
- If a theme contradicts an existing instruction, do **not** silently overwrite —
  describe the conflict in the PR body and leave the existing text in place.
- Only modify files under `.github/` (and `AGENTS.md`). Do not touch source code,
  manifests, or anything else.

If the repo has no `.github/` customization files at all, bootstrap a starter
`.github/copilot-instructions.md` from the top themes.

## 7. Open or update the pull request

Use the **create-pull-request** safe output to propose the changes on a stable
branch named `automated/update-instructions-from-pr-reviews`. Because the branch
name is preserved across runs, an existing unmerged PR on that branch is updated
in place; if the previous PR was already merged, a fresh one is opened.

Keep the PR body short and scannable. Lead with a one-line link back to this
workflow so a reader knows where the proposed edits came from, then use compact
bulleted sections — no big tables, no per-file summaries. Structure it as:

- A leading line linking to the source of these changes (this workflow), e.g.
  `Proposed by the [Update Instructions From PR Reviews](.github/workflows/update-instructions-from-pr-reviews.md) workflow.`
- **Settings** — a short bullet list:
  - Repo: the source `owner/repo` that was mined.
  - Range: the effective PR range (e.g. `since 2026-01-01` or `last 6 months`).
  - A link to the branch we've pushed our updates to. The text should include the short commit ID.
- **Stats** — a short bullet list:
  - Signal ratio: kept-vs-dropped comments.
  - Resolved PRs: the number of merged PRs examined.
- **PR comment themes** — one short bullet per promoted theme, each citing its
  source PRs (e.g. `Wrap errors with context — _#7012, #7034_`), with links to each PR.
- **Troubleshooting** — only when relevant (the issue-fallback case below), a
  short bullet list pointing the maintainer at how to enable real PRs:
  - https://github.github.com/gh-aw/reference/faq/#why-is-my-create-pull-request-workflow-failing-with-github-actions-is-not-permitted-to-create-or-approve-pull-requests

Keep each theme bullet to a single line, but always preserve its source-PR
citations so the maintainer can audit why each rule was added.

If the org blocks Actions from creating pull requests, the safe-output job
automatically falls back to opening an **issue** (assigned to `copilot`) with a
link to the pushed branch, so the proposed edits are never lost and Copilot can
open the PR from there. You don't need to do anything special — write the body
the same way; it's reused for the fallback issue.

## 8. Record the run

Write the current date (the high-water mark of mined PRs) to `last-run.txt` in
**cache memory** so the next scheduled run only processes newer PRs.

## Guardrails

- Never commit or push directly — all changes flow through the safe output.
- Cluster first; never write one rule per individual comment.
- Never cite zero source PRs on a promoted rule.
- Do not declare success from raw comment counts alone — judge signal quality.
- If there is no qualifying signal, make no edits and open no PR.
