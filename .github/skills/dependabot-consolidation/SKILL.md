---
name: dependabot-consolidation
license: MIT
metadata:
  version: "1.0"
description: >-
  **WORKFLOW SKILL** — Consolidates all open Dependabot PRs and security alerts in
  Azure/azure-dev into one maintained PR. Reuses an open consolidation PR, validates updates,
  then comments on and closes only incorporated Dependabot PRs.

  INVOKES: gh CLI, git CLI, ecosystem package managers, build/test commands, ask_user.

  USE FOR: consolidate dependabot PRs, group dependency updates, fix all dependabot alerts,
  update the dependency consolidation PR, dependency security sweep.

  DO NOT USE FOR: one named dependency, reviewing or merging PRs, dismissing alerts,
  Renovate PRs, or work outside Azure/azure-dev.
---

# dependabot-consolidation

Consolidate every open Dependabot PR and alert in `Azure/azure-dev` into one maintained PR.

## Execution

1. Read and follow [references/workflow.md](references/workflow.md) completely.
2. Require a clean `Azure/azure-dev` checkout and working GitHub access.
3. Inventory alerts, open Dependabot PRs, changed projects, and an existing marked PR.
4. Show the complete mutation preview through `ask_user`.
5. Apply grouped updates, validate each project, and push one branch.
6. Create or update the marked PR.
7. Comment on and close only source PRs proven incorporated.

## Non-Negotiable Rules

- Never skip the confirmation, merge the PR, dismiss alerts, force-push, or overwrite user changes.
- Keep blocked or unverified Dependabot PRs open.
- Stop if alert access is unavailable or multiple marked consolidation PRs exist.

## Exit Criteria

Link the created or updated PR and report covered alerts, closed source PRs, validation, and blockers.
