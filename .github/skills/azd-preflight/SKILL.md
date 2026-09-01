---
name: azd-preflight
license: MIT
metadata:
  version: "1.3"
  # Bump major on breaking prompt/trigger changes; bump minor on new references or fix strategies.
description: >-
  **WORKFLOW SKILL** — Runs `mage preflight` from `cli/azd/` and auto-fixes failures.
  Covers linting, formatting, copyright, spelling, build, tests, and generated pipeline action pins.
  Iterates fix-then-rerun cycles until all checks pass.

  INVOKES: mage CLI, go CLI, golangci-lint, cspell, gh CLI, bash/sh, ask_user.

  USE FOR: azd preflight, run preflight, preflight checks, pre-commit checks, azd lint,
  azd build and test, validate azd changes, check azd code quality, mage preflight.

  DO NOT USE FOR: deploying azd (use azure-deploy), changelog (use changelog-generation),
  creating PRs (use pull-request), running only unit tests without the full suite.
---

# azd-preflight

Runs the full azd preflight suite and auto-fixes failures.

## Overview

The azd preflight suite (`mage preflight`) validates code quality across 9 checks before
changes are submitted. This skill runs the suite, parses failures, applies automated fixes,
and re-runs until all checks pass — or escalates to the user when a fix requires human judgment.

{{ references/preflight-checks.md }}

{{ references/workflow.md }}

{{ references/output-and-errors.md }}

## Exit Criteria

- All 9 preflight checks pass (or user explicitly chose to skip specific checks)
- Every changed `CHANGELOG.md` passes its targeted spell check
- When the generated pipeline action manifest changes, `azure-dev.ymlt` and its snapshots are synchronized
- All auto-applied fixes are saved to disk (not staged or committed — the user decides when to commit)
- A clear summary of what passed, what was fixed, and what was skipped is displayed
