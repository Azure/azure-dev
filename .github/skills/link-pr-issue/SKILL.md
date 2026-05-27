---
name: link-pr-issue
license: MIT
metadata:
  version: "1.0"
description: >-
  **WORKFLOW SKILL** — Creates a GitHub issue from a PR's title and description,
  then links the PR to the new issue via the `Fixes #NNN` keyword.
  Designed to satisfy CI governance gates that require PRs to reference an issue.

  INVOKES: gh CLI, ask_user.

  USE FOR: link PR to issue, create issue for PR, PR missing issue, fix CI governance,
  create and link issue, PR needs issue, link issue to PR, pr issue gate,
  create tracking issue for PR.

  DO NOT USE FOR: changelog generation (use changelog-generation),
  code review (use code-review), creating PRs from issues.
---

# link-pr-issue

Creates a GitHub issue derived from a pull request and links the PR back to it.
This satisfies CI governance gates that require every PR to reference an issue.

## Overview

Many PRs are created without a linked issue — quick fixes, dependency bumps,
external contributions, or automated changes. The repo's CI gate checks for
an issue reference and blocks merge when one is missing. This skill automates
the boilerplate of creating a matching issue and editing the PR body.

{{ references/workflow.md }}

## Exit Criteria

- A new GitHub issue exists with a title and body derived from the PR.
- The PR body contains `Fixes #<issue_number>`.
- The user has confirmed the issue content before creation and the PR body edit before linking.
