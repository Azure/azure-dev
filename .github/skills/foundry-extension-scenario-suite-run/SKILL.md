---
name: foundry-extension-scenario-suite-run
license: MIT
metadata:
  version: "1.3"
  # Bump major on breaking prompt/trigger changes; bump minor on new references or selection rules.
  # 1.0: initial split from the scenarios README's fleet prompt; full / tag / tier sweep that is
  # NOT tied to a PR diff. Per-scenario driving is delegated to the foundry-extension-scenario-worker agent and
  # specified once in the scenarios' driving-mechanics.md.
  # 1.1: close filtered Tier 2 selections over setup/teardown and assign run-unique
  # per-scenario instances.
  # 1.2: shorten description to Copilot's 1,024-character limit; the INVOKES
  # tool list moved to the body below.
description: >-
  **WORKFLOW SKILL** — Runs the azure.ai.agents extension's cli-interactive-tester scenarios as
  a full or tag/tier-filtered sweep NOT tied to a PR diff (Tier 0 free; Tier 1b/2 cost-gated).
  Discovers scenarios via list_scenarios, drives them concurrently via
  foundry-extension-scenario-worker agents, then writes an aggregate report. Dispatched by
  foundry-extension-scenario-orchestrator, or triggered directly.

  USE FOR: run the whole suite, run all scenarios, smoke-test the extension, a tag-filtered
  sweep (e.g. "run every `init` scenario"), a tier sweep (e.g. "run all of Tier 0"), a
  scheduled/periodic full regression not scoped to a diff.

  DO NOT USE FOR: PR- or diff-scoped selection (foundry-extension-scenario-pr-regression maps
  changed files to tags and comments on the PR), authoring/editing scenarios without running
  them — no Azure cost (foundry-extension-scenario-authoring), azd core preflight
  (azd-preflight), changelog (changelog-generation), creating PRs (pull-request), scenarios for
  other extensions.
---

# foundry-extension-scenario-suite-run

Run only when explicitly requested. Preserve scenario confirmation and Azure cost consent.

Before acting, read and follow the [extension-owned workflow](../../../cli/azd/extensions/azure.ai.agents/skills/foundry-extension-scenario-suite-run/workflow.md).
Stop and report the missing workflow if it cannot be read; do not invent a fallback.
Resolve its relative links from the workflow file, not from this entry point.
