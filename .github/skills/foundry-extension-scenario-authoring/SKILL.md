---
name: foundry-extension-scenario-authoring
license: MIT
metadata:
  version: "1.5"
  # Bump major on breaking prompt/trigger changes; bump minor on new references or authoring rules.
  # 1.0: initial authoring + validation skill for the azure.ai.agents cli-interactive-tester
  # scenarios. Taxonomy (tiers/tags/profile/hooks/fixtures/requires) is single-sourced in the
  # scenarios README; this skill adds the authoring procedure and a no-execution validation loop.
  # 1.1: require deterministic input for every executed invoke command.
  # 1.2: document the run-scoped identity placeholders supplied by orchestration.
  # 1.3: shorten description to Copilot's 1,024-character limit; procedural
  # detail (INVOKES tool list) moved to the body below.
  # 1.4: recognize shared model version/SKU and bounded Foundry project placeholders.
description: >-
  **WORKFLOW SKILL** — Authors and validates cli-interactive-tester scenarios for the
  azure.ai.agents extension: writes a new scenario YAML (or fixes an existing one) so it
  follows the tier/tag/hook/fixtures/requires conventions and goals-are-the-contract judging
  rules, then lint-validates it via list_scenarios (never start_session — no Azure cost).
  Typically driven through the foundry-extension-scenario-author agent.

  USE FOR: write a new scenario, add coverage for a command/flag, fix a scenario's
  tags/hooks/requires, add a fixture, close a coverage gap flagged by a PR regression run.

  DO NOT USE FOR: RUNNING scenarios or a suite — never drives a scenario or incurs Azure cost
  (use foundry-extension-scenario-suite-run for a sweep, or foundry-extension-scenario-pr-regression
  for a PR), driving a single scenario (foundry-extension-scenario-worker), azd core preflight
  (azd-preflight), changelog (changelog-generation), creating PRs (pull-request), scenarios for
  other extensions.
---

# foundry-extension-scenario-authoring

Author and lint scenarios only. Never run scenarios or provision resources.

Before acting, read and follow the [extension-owned workflow](../../../cli/azd/extensions/azure.ai.agents/skills/foundry-extension-scenario-authoring/workflow.md).
Stop and report the missing workflow if it cannot be read; do not invent a fallback.
Resolve its relative links from the workflow file, not from this entry point.
