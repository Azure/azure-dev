---
name: sensei
license: MIT
metadata:
  version: "1.0"
  # Bump major on breaking prompt/trigger changes; bump minor on new references or improvements.
description: >-
  **WORKFLOW SKILL** — Evaluates and improves agent skill quality in `.github/skills/` using waza.
  Scores skill compliance (frontmatter, triggers, anti-triggers, eval coverage),
  suggests improvements, runs eval suites, and iterates until target quality is reached.

  INVOKES: waza CLI, ask_user.

  USE FOR: evaluate skill, improve skill, check skill compliance, run skill evals,
  sensei, skill quality, validate skills, skill audit, waza check, waza run.

  DO NOT USE FOR: creating new skills from scratch (use waza new), deploying code
  (use azure-deploy), code review (use code-review).
---

# sensei

Evaluates and improves agent skill quality using waza.

## Overview

Sensei automates the evaluation and improvement of agent skills in this repository.
It uses [waza](https://github.com/microsoft/waza) to score compliance, run eval suites,
and iteratively improve skills until they meet quality targets.

## Prerequisites

- **waza**: `go install github.com/microsoft/waza/cmd/waza@latest`
  or `azd ext install microsoft.azd.waza`

{{ references/workflow.md }}

## Exit Criteria

- All target skills have been checked and optionally improved
- Compliance scores and eval results are reported
- Changes saved to disk (user decides when to commit)
