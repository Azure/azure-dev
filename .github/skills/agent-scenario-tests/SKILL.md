---
name: agent-scenario-tests
license: MIT
metadata:
  version: "1.0"
  # Bump major on breaking prompt/trigger changes; bump minor on new references or mapping rules.
description: >-
  **WORKFLOW SKILL** — Runs the azure.ai.agents extension's cli-interactive-tester
  scenarios locally as a PR regression check. Resolves the current branch's PR,
  maps changed files to impacted scenario tags, drives the matching scenarios
  through the cli-interactive-tester MCP server, and posts a results comment on the PR.

  INVOKES: git CLI, gh CLI, cli-interactive-tester MCP tools (list_scenarios,
  load_scenario, run_pre_hooks, start_session, send_action, finish_session,
  run_post_hooks), ask_user.

  USE FOR: run agent scenarios, scenario regression check, cli-interactive-tester,
  test agents extension PR, run impacted scenarios, check agents extension for regressions,
  agent scenario tests, validate azure.ai.agents change.

  DO NOT USE FOR: azd core preflight (use azd-preflight), changelog (use changelog-generation),
  creating PRs (use pull-request), authoring brand-new scenarios from scratch without a code
  change, running scenarios for any extension other than azure.ai.agents.
---

# agent-scenario-tests

Runs the `azure.ai.agents` extension's interactive CLI scenarios as a **local**
PR regression gate and reports the results back on the pull request.

## Overview

The `azure.ai.agents` extension ships goal-based scenarios for the
[cli-interactive-tester](https://github.com/coreai-microsoft/cli-interactive-tester)
MCP server under `cli/azd/extensions/azure.ai.agents/tests/cli-interactive-tester-scenarios/`.
These scenarios are **never run in CI** — they need the tester MCP server, a populated
`profile.local.yaml`, manual `az`/`gh` login, and (for Tier 2) real Azure resources.

This skill is the **opt-in, run-locally** flow a PR submitter uses to check their change
for regressions. It:

1. Resolves the current branch's PR link.
2. Maps the PR's changed files to the impacted scenario **tag set** (`cmd:*` / `tier:*`).
3. Enumerates and drives only the impacted scenarios through the tester.
4. Posts a per-scenario results comment back on the PR.

It is cost- and side-effect-aware: Tier 0 is free/offline, Tier 1 needs Azure auth but
provisions nothing, and **Tier 2 incurs Azure cost and is only run after explicit user
confirmation**.

> This skill drives scenarios **deliberately, with user consent**. That is different from
> the extension's `AGENTS.md` rule that coding agents must not invoke scenarios on their
> own during ordinary work — here the user has explicitly asked for a scenario run.

{{ references/prerequisites.md }}

{{ references/workflow.md }}

{{ references/impact-mapping.md }}

{{ references/running-scenarios.md }}

{{ references/reporting.md }}

## Exit Criteria

- The current branch's PR was resolved (or the user supplied one / chose to skip the comment).
- The impacted scenario set was derived from the PR diff and **confirmed by the user**
  (including an explicit cost acknowledgement before any Tier 2 run).
- Every selected scenario was driven to completion with a recorded PASS/FAIL, duration, and
  any findings, and a `FINAL-REPORT.md` was written under `.reports/<run-timestamp>/`.
- A results comment was posted on the PR (unless the user opted out), and any Tier 2 run was
  followed by `2Z-teardown-down` so no Azure resources are left running.
