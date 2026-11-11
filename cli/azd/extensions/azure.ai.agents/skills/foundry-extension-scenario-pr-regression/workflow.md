# foundry-extension-scenario-pr-regression

Runs the `azure.ai.agents` extension's interactive CLI scenarios as a **local**
PR regression gate and reports the results back on the pull request.

## Overview

The `azure.ai.agents` extension ships goal-based scenarios for the
[cli-interactive-tester](https://github.com/coreai-microsoft/cli-interactive-tester)
MCP server under `cli/azd/extensions/azure.ai.agents/tests/cli-interactive-tester-scenarios/`.
These scenarios are **never run in CI** — they need the tester MCP server, a populated
`profile.local.yaml`, manual `az`/`gh` login, and (for Tier 2) real Azure resources.

**Tools invoked**: git CLI, gh CLI, cli-interactive-tester MCP tools (`list_scenarios`,
`load_scenario`, `run_pre_hooks`, `start_session`, `send_action`, `finish_session`,
`run_post_hooks`), the `foundry-extension-scenario-worker` agent, and `ask_user`.

This skill is the **opt-in, run-locally** flow a PR submitter uses to check their change
for regressions. It:

1. Resolves the current branch's PR link.
2. Maps the PR's changed files to the impacted scenario **tag set** (`cmd:*` / `tier:*`).
3. Enumerates and drives only the impacted scenarios through the tester.
4. Posts a per-scenario results comment back on the PR.

It is cost- and side-effect-aware: Tier 0 is free/offline, Tier 1 needs Azure auth but
provisions nothing, **Tier 1b** (`verify-deploy`) provisions per-scenario Azure resources to
verify Tier 1 scaffolds actually deploy, and **Tier 2** incurs Azure cost for cloud-feature
testing — both Tier 1b and Tier 2 are only run after explicit user confirmation.

This skill selects **what** to run (PR impact → tags) and owns the PR reporting. It does not
re-document **how** a scenario is driven — that is the executor spec at
`cli/azd/extensions/azure.ai.agents/tests/cli-interactive-tester-scenarios/driving-mechanics.md`,
which the spawned **foundry-extension-scenario-worker** agent follows for each scenario. This skill is the
selection-strategy half of the **foundry-extension-scenario-orchestrator** front door; the orchestrator may
dispatch it, or it may trigger directly.

> This skill drives scenarios **deliberately, with user consent**. That is different from
> the extension's `AGENTS.md` rule that coding agents must not invoke scenarios on their
> own during ordinary work — here the user has explicitly asked for a scenario run.

Read and follow [prerequisites](./references/prerequisites.md).

Read and follow [workflow](./references/workflow.md).

Read and follow [impact mapping](./references/impact-mapping.md).

## Execution mechanics (single source)

Per-scenario driving — path style, the `requires:` gate, fail-loud rules, parallelism and
ordering, and capture — is **not** restated here. It lives once in the executor spec:
[`driving-mechanics.md`](../../tests/cli-interactive-tester-scenarios/driving-mechanics.md)
(repo path
`cli/azd/extensions/azure.ai.agents/tests/cli-interactive-tester-scenarios/driving-mechanics.md`).
Fan product phases out to **foundry-extension-scenario-worker** agents, then use cleanup-phase
workers for deferred Tier 1b post hooks per that spec. This skill's job is selection
(impact → tags), gating, and reporting — not driving.

Read and follow [reporting](./references/reporting.md).

## Exit Criteria

- The target PR was resolved before the build, and a posting run used a clean checkout whose
  tested SHA exactly matched that PR's head SHA (or the user chose a local-only, non-posting run).
- The impacted scenario set was derived from the PR diff and **confirmed by the user**
  (including an explicit cost acknowledgement before any Tier 1b or Tier 2 run).
- Every selected scenario was driven to completion with a recorded PASS/FAIL/SKIPPED, duration,
  product and cleanup findings, and a `FINAL-REPORT.md` was written under `.reports/<run-id>/`.
- Every changed file that matched no impact rule was listed as a coverage gap in the plan and
  final output rather than silently omitted.
- Scenarios with a `requires:` field whose prerequisite did not PASS are marked ⏭️ SKIPPED
  (not FAIL) with a clear reason.
- A results comment was posted on the PR (unless the user opted out), and any Tier 1b/Tier 2
  run was followed by appropriate teardown. Failed or pending cleanup is reported explicitly.
