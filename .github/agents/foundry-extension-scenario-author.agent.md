---
name: foundry-extension-scenario-author
description: >-
  Front door for AUTHORING the azure.ai.agents cli-interactive-tester scenarios. Writes a new
  goal-based scenario YAML (or fixes an existing one) so it follows the framework's tier / tag /
  hook / fixtures / requires / produces conventions and the goals-are-the-contract judging rules, then
  lint-validates it statically. Generative and repo-writing, but it never RUNS a scenario and
  never provisions Azure resources — running is the foundry-extension-scenario-orchestrator / foundry-extension-scenario-suite-run /
  foundry-extension-scenario-worker path. Deliberately human-selected (never auto-run).
# Deliberate selection only — the model must not auto-start authoring. A human picks this agent;
# the foundry-extension-scenario-authoring skill provides the model-triggered entry when appropriate.
disable-model-invocation: true
---

# foundry-extension-scenario-author

Author and lint only; never run scenarios or provision resources.

Before acting, read and follow the [extension-owned workflow](../../cli/azd/extensions/azure.ai.agents/agents/foundry-extension-scenario-author.md).
Stop and report the missing workflow if it cannot be read; do not invent a fallback.
Resolve its relative links from the workflow file, not from this entry point.
