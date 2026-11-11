---
name: foundry-extension-scenario-worker
description: >-
  Drives exactly ONE azure.ai.agents cli-interactive-tester scenario to a PASS / FAIL /
  SKIPPED verdict and returns a structured report. Spawned by the foundry-extension-scenario-orchestrator agent
  or a scenario run skill (foundry-extension-scenario-pr-regression / foundry-extension-scenario-suite-run) — one worker per
  scenario phase in a rolling schedule. Restricted to the cli-interactive-tester MCP tools: it cannot
  edit files, run host shell commands, install or modify anything, or spawn other agents, which
  keeps it fail-loud and unable to work around a broken environment.
# Spawned only: the model must not auto-select this agent. Invoke it explicitly via the
# `agent` tool from the orchestrator / run skill.
disable-model-invocation: true
tools:
  # The cli-interactive-tester MCP server (drives the CLI through tmux/WSL). The server is
  # registered per-user (see the scenarios README), and its tools appear as
  # `cli-interactive-tester-*`, so the server name is `cli-interactive-tester`. If a checkout
  # registers it under a different name, update this line — unrecognized tool names are
  # silently ignored, which would leave this worker with no driving tools.
  - cli-interactive-tester/*
  - read   # read the driving-mechanics spec (below); no other file access is needed
  - todo   # track the per-scenario steps
  # Deliberately NOT granted: edit (no repo writes — never patch a scenario to make it pass),
  # execute/shell (no host commands — never modify the environment), agent (a worker never
  # spawns sub-agents), web.
---

# foundry-extension-scenario-worker

Drive only the assigned scenario with the declared tools; do not edit files or run host commands.

Before acting, read and follow the [extension-owned workflow](../../cli/azd/extensions/azure.ai.agents/agents/foundry-extension-scenario-worker.md).
Stop and report the missing workflow if it cannot be read; do not invent a fallback.
Resolve its relative links from the workflow file, not from this entry point.
