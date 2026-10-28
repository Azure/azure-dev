---
# cspell:ignore triaging worktree dedup
name: azd-code-reviewer
description: "Reviews GitHub pull requests by applying multiple specialist lenses (security, Go expert, architect, testing, UX, docs, PM, Azure, novice customer), triaging findings for signal-to-noise, and returning structured inline comments. Use for: review PR, look at this PR, give feedback on PR, check this PR, code review, PR feedback."
license: MIT
metadata:
  version: "1.2"
---

# Code review

Reviews GitHub PRs by applying a panel of specialist review lenses to the diff, filtering for high-signal findings, and returning structured comments for the host to post.

## Operating environment

This skill usually runs inside an automated review harness (e.g., Copilot Code Review). It assumes:

- **The diff and PR context are already provided** by the harness. In an interactive invocation without that context, use read-only `git` and `gh` commands to discover the current branch diff and PR metadata. Do not change branches or the worktree.
- **No interactive user.** No `ask the user` flows, no walkthroughs, no "want to proceed?" gates. The skill produces findings and returns them.
- **No build or test execution.** Test code is reviewed statically.
- **The harness handles posting.** The skill returns structured findings; the harness creates the review.
- **Extension contract reviews use a companion skill.** Keep `extension-user-contract-review` available alongside
  this skill when distributing it outside this repository.

If the host is interactive (e.g., a chat agent invoking the skill manually), the same workflow still works — interactive steps simply don't apply.

## Workflow

### Step 1: Read the diff and PR context

Use whatever PR metadata, diff, and changed-file list the harness provides. In an interactive invocation where these are missing, discover them with read-only `git` and `gh` commands. If supplementary context such as linked issues or prior review comments is available, use it.

### Step 2: Mandatory extension contract pass

For any PR changing `cli/azd/extensions/**`, including fork PRs, you MUST load and apply the
[extension-user-contract-review skill](../extension-user-contract-review/SKILL.md) and its full review procedure
before the general review lenses. Do not make invocation depend on already recognizing a contract change.
Inspect every changed extension file, track coverage and existing threads, and preserve its findings for shared
triage. If the skill or required evidence is unavailable, report the contract pass as blocked rather than skipping it.
Skip this pass only when the complete file list contains no extension changes.

If added files are under a top-level path in `cli/azd/extensions/` that did not exist before the PR, also load [new-extension.md](new-extension.md) and apply its onboarding review. An added `extension.yaml` is enough to trigger the new-extension review because a dependency-only extension pack may have no module or entry point.

### Step 3: Apply review lenses

Load [reviewers.md](reviewers.md) and apply each of the 9 fixed lenses to the diff. Then scan the changed files for the domain signals listed in `reviewers.md` and apply matching domain lenses. Each lens produces zero or more findings in the structured format defined in `reviewers.md`.

**The reviewing agent is the same LLM** — these lenses are _perspectives to apply sequentially or in parallel_, not separate subagents. The host may parallelize lens application if supported, but a single-pass application of all lenses to the same diff is equally valid.

### Step 4: Triage findings

Load [findings.md](findings.md). Apply the self-reflection pass: drop low-signal findings according to the dismissal triggers, merge duplicates across lenses, group by file, and sort by line.

### Step 5: Voice and format

Still in `findings.md`. Apply the voice rules to every finding's text. Build the review body (one sentence + optional bullets for non-line findings). Return the final findings in the format the harness expects.

### Step 6: Mandatory contract completion audit

For extension PRs, reconcile the contract coverage ledger with the complete changed-file list and the actual outgoing
comments. Every distinct contract change must have one inline comment or a matching existing thread. Do not drop
contract comments to satisfy an ordinary finding budget. Expose the companion skill's completion status and counts
in the review body or supported result channel. An incomplete pass must say `blocked`, not imply a clean review.

## Principles

- **Silence over noise.** If a lens has nothing meaningful to say, it returns nothing. No invented concerns. Fewer, better comments is the goal.
- **Codebase context matters.** Review the change in context — read surrounding code, not just the diff. Cross-file logic errors are the most common real bugs.
- **Independence.** The reviewing agent builds its own understanding. It does not share assumptions with whatever generated the code.
- **Findings must be useful.** Critical, suggestion, and nit comments must give the author something specific to act on. Praise must name a concrete pattern worth preserving.

## Bundled resources

| File                 | Contents                                                                                       |
| -------------------- | ---------------------------------------------------------------------------------------------- |
| `reviewers.md`       | 9 fixed lens definitions, dynamic domain detection rules, shared ground rules, findings format |
| `findings.md`        | Self-reflection triage, dismissal triggers, merge/dedup, voice rules, review body guidance     |
| `new-extension.md`   | New first-party extension scaffolding, repository wiring, release, and handoff checks          |
| [extension-user-contract-review](../extension-user-contract-review/SKILL.md) | Companion skill for extension contract changes and per-thread experiences spec tracking |
