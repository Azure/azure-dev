---
name: foundry-extension-scenario-pr-regression
license: MIT
metadata:
  version: "2.2"
  # Bump major on breaking prompt/trigger changes; bump minor on new references or mapping rules.
  # 2.0: renamed from agent-scenario-tests; execution mechanics moved to the scenarios'
  # driving-mechanics.md; per-scenario driving delegated to the foundry-extension-scenario-worker agent.
  # 2.1: shorten description to Copilot's 1,024-character limit; the INVOKES
  # tool list moved to the body below.
description: >-
  **WORKFLOW SKILL** — Runs the azure.ai.agents extension's cli-interactive-tester scenarios
  locally as a PR-scoped regression check (Tier 0 is free/offline; Tier 1b/Tier 2 incur Azure
  cost). Resolves the current branch's PR, maps changed files to impacted scenario tags, drives
  matching scenarios via
  foundry-extension-scenario-worker agents, and posts a results comment on the PR. Typically
  dispatched by foundry-extension-scenario-orchestrator, but can trigger directly.

  USE FOR: run agent scenarios for a PR, scenario regression check, test agents extension PR,
  run impacted scenarios, check for regressions before merge, validate an azure.ai.agents diff.

  DO NOT USE FOR: running the whole suite or a tag/tier sweep not tied to a diff
  (foundry-extension-scenario-suite-run), authoring/editing scenarios without running them — no
  Azure cost (foundry-extension-scenario-authoring), azd core preflight (azd-preflight),
  changelog (changelog-generation), creating PRs (pull-request), scenarios for other
  extensions.
---

# foundry-extension-scenario-pr-regression

Run only when explicitly requested. Preserve scenario confirmation and Azure cost consent.

Before acting, read and follow the [extension-owned workflow](../../../cli/azd/extensions/azure.ai.agents/skills/foundry-extension-scenario-pr-regression/workflow.md).
Stop and report the missing workflow if it cannot be read; do not invent a fallback.
Resolve its relative links from the workflow file, not from this entry point.
