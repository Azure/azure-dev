---
name: foundry-extension-scenario-orchestrator
description: >-
  Front door for RUNNING the azure.ai.agents cli-interactive-tester scenarios. Coordinates a
  whole run — verifies prerequisites and the azd binary, gates Azure cost, validates the recipe,
  fans scenarios out to foundry-extension-scenario-worker agents in the right order, and hands off reporting. Routes
  the selection strategy to a run skill: foundry-extension-scenario-pr-regression for a PR/diff regression check, or
  foundry-extension-scenario-suite-run for a full or tag/tier sweep. Deliberately human-selected (never auto-run),
  honoring the extension AGENTS.md rule that scenarios are never run automatically.
# Deliberate selection only — the model must not auto-start a scenario run. A human picks this
# agent (matching agentic-workflows.agent.md and the extension AGENTS.md "never run
# automatically" rule).
disable-model-invocation: true
---

# foundry-extension-scenario-orchestrator

Requires explicit user selection. Preserve the prerequisite, cost, and cleanup gates.

Before acting, read and follow the [extension-owned workflow](../../cli/azd/extensions/azure.ai.agents/agents/foundry-extension-scenario-orchestrator.md).
Stop and report the missing workflow if it cannot be read; do not invent a fallback.
Resolve its relative links from the workflow file, not from this entry point.
