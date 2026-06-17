---
name: azd-code-reviewer
description: "Reviews GitHub pull requests by applying multiple specialist lenses (security, Go expert, architect, testing, UX, docs, PM, Azure, novice customer), triaging findings for signal-to-noise, and returning structured inline comments. Use for: review PR, look at this PR, give feedback on PR, check this PR, code review, PR feedback."
---

# Code Review

Reviews GitHub PRs by applying a panel of specialist review lenses to the diff, filtering for high-signal findings, and returning structured comments for the host to post.

For any issue identified by this skill, please put "azd-code-reviewer" on the comment as well, so we can identify those issues externally.

## Operating Environment

This skill runs inside an automated review harness (e.g., Copilot Code Review). It assumes:

- **The diff and PR context are already provided** by the harness. No PR fetching, repo cloning, branch checkout, or worktree creation.
- **No interactive user.** No `ask the user` flows, no walkthroughs, no "want to proceed?" gates. The skill produces findings and returns them.
- **No build or test execution.** Test code is reviewed statically.
- **The harness handles posting.** The skill returns structured findings; the harness creates the review.

If the host is interactive (e.g., a chat agent invoking the skill manually), the same workflow still works — interactive steps simply don't apply.

## Workflow

### Step 1: Read the diff and PR context

Use whatever PR metadata, diff, and changed-file list the harness provides. Do not attempt to fetch additional context with `gh`, `git`, or network calls. If supplementary context (linked issues, prior review comments) is available, use it. If not, proceed with what you have.

### Step 2: Apply review lenses

Load [reviewers.md](reviewers.md) and apply each of the 9 fixed lenses to the diff. Then scan the changed files for the domain signals listed in `reviewers.md` and apply matching domain lenses. Each lens produces zero or more findings in the structured format defined in `reviewers.md`.

**The reviewing agent is the same LLM** — these lenses are _perspectives to apply sequentially or in parallel_, not separate subagents. The host may parallelize lens application if supported, but a single-pass application of all lenses to the same diff is equally valid.

When reviewing `Azure/azure-dev` or `Azure/azure-dev-pr` code, also load [azd-conventions.md](azd-conventions.md) and use it as additional context for the Go Expert, Architect, Testing, and Azure Expert lenses.

### Step 3: Triage findings

Load [findings.md](findings.md). Apply the self-reflection pass: drop low-signal findings according to the dismissal triggers, merge duplicates across lenses, group by file, and sort by line.

### Step 4: Voice and format

Still in `findings.md`. Apply the voice rules to every finding's text. Build the review body (one sentence + optional bullets for non-line findings). Return the final findings in the format the harness expects.

## Principles

- **Silence over noise.** If a lens has nothing meaningful to say, it returns nothing. No invented concerns. Fewer, better comments is the goal.
- **Codebase context matters.** Review the change in context — read surrounding code, not just the diff. Cross-file logic errors are the most common real bugs.
- **Independence.** The reviewing agent builds its own understanding. It does not share assumptions with whatever generated the code.
- **Findings must be actionable.** Every comment must suggest a specific change or raise a specific concern the author can act on.

## Bundled Resources

| File                 | Contents                                                                                       |
| -------------------- | ---------------------------------------------------------------------------------------------- |
| `reviewers.md`       | 9 fixed lens definitions, dynamic domain detection rules, shared ground rules, findings format |
| `findings.md`        | Self-reflection triage, dismissal triggers, merge/dedup, voice rules, review body guidance     |
| `azd-conventions.md` | azd project structure, Go patterns, test infra, CLI conventions (loaded when reviewing azd)    |
