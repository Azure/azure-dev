---
name: foundry-extension-scenario-suite-run
license: MIT
metadata:
  version: "1.1"
  # Bump major on breaking prompt/trigger changes; bump minor on new references or selection rules.
  # 1.0: initial split from the scenarios README's fleet prompt; full / tag / tier sweep that is
  # NOT tied to a PR diff. Per-scenario driving is delegated to the foundry-extension-scenario-worker agent and
  # specified once in the scenarios' driving-mechanics.md.
  # 1.1: close filtered Tier 2 selections over setup/teardown and assign run-unique
  # per-scenario instances.
description: >-
  **WORKFLOW SKILL** — Runs the azure.ai.agents extension's cli-interactive-tester scenarios as
  a **full or tag/tier-filtered sweep** that is *not* tied to a PR diff. Discovers scenarios via
  list_scenarios, gates Azure cost, and schedules them through adaptive rolling concurrency,
  then writes an aggregate report. Typically dispatched by the foundry-extension-scenario-orchestrator agent, but
  can trigger directly.

  INVOKES: cli-interactive-tester MCP tools (list_scenarios, load_scenario, run_pre_hooks,
  start_session, send_action, finish_session, run_post_hooks), the foundry-extension-scenario-worker agent,
  ask_user.

  USE FOR: run the whole scenario suite, run all scenarios, smoke-test the azure.ai.agents
  extension end to end, a tag-filtered sweep (e.g. "run every `init` scenario", "run everything
  tagged `parallel-safe`"), a tier sweep (e.g. "run all of Tier 0", "run Tier 2"), a scheduled /
  periodic full regression not scoped to a diff.

  DO NOT USE FOR: PR- or diff-scoped selection (use foundry-extension-scenario-pr-regression — it maps changed
  files to impacted tags and comments on the PR), authoring or editing scenarios (use
  foundry-extension-scenario-authoring), azd core preflight (use azd-preflight), changelog (use
  changelog-generation), creating PRs (use pull-request), scenarios for any extension other than
  azure.ai.agents.
---

# foundry-extension-scenario-suite-run

Runs the `azure.ai.agents` extension's interactive CLI scenarios as a **full or filtered
sweep** and writes an aggregate report. Selection here is by **tag / tier**, not by a PR diff —
that is the one thing that distinguishes this skill from `foundry-extension-scenario-pr-regression`.

## Overview

The `azure.ai.agents` extension ships goal-based scenarios for the
[cli-interactive-tester](https://github.com/coreai-microsoft/cli-interactive-tester)
MCP server under `cli/azd/extensions/azure.ai.agents/tests/cli-interactive-tester-scenarios/`.
They are **never run in CI** — they need the tester MCP server, a populated
`profile.local.yaml`, manual `az`/`gh` login, and (for Tier 1b / Tier 2) real Azure resources.

This skill is the **opt-in, run-locally** flow for running many scenarios at once: the whole
suite, or a subset selected by `tags:` (a command like `cmd:init`, a trait like `parallel-safe`,
or a tier like `tier:0`). It:

1. Resolves the sweep's **tag/tier filter** from the user's request (or runs everything).
2. Enumerates the matching scenarios via `list_scenarios`.
3. Gates Azure cost (Tier 1b / Tier 2), then drives scenarios as their dependencies become ready.
4. Writes an aggregate `FINAL-REPORT.md` (no PR comment unless the user asks).

It is cost- and side-effect-aware: Tier 0 is free/offline, Tier 1 needs Azure auth but
provisions nothing, **Tier 1b** (`verify-deploy`) provisions per-scenario Azure resources, and
**Tier 2** incurs Azure cost — Tier 1b and Tier 2 run only after explicit user confirmation.

This skill selects **what** to run (tag/tier filter) and owns the aggregate report. It does not
re-document **how** a scenario is driven — that is the executor spec at
`cli/azd/extensions/azure.ai.agents/tests/cli-interactive-tester-scenarios/driving-mechanics.md`,
which the spawned **foundry-extension-scenario-worker** agent follows for each scenario. This skill is one of the
two selection strategies behind the **foundry-extension-scenario-orchestrator** front door; the orchestrator may
dispatch it, or it may trigger directly.

> This skill drives scenarios **deliberately, with user consent**. That is different from the
> extension's `AGENTS.md` rule that coding agents must not invoke scenarios on their own during
> ordinary work — here the user has explicitly asked for a scenario run.

## Prerequisites

The prerequisites, profile merge, and `session_vars` derivation are **identical** to the
PR-regression flow and are single-sourced there. Follow
[`../foundry-extension-scenario-pr-regression/references/prerequisites.md`](../foundry-extension-scenario-pr-regression/references/prerequisites.md)
(repo path `.github/skills/foundry-extension-scenario-pr-regression/references/prerequisites.md`) before doing
anything else. In particular: verify the tester MCP server and `profile.local.yaml`, and derive
the base `session_vars` (profile merge + `run_id` + `shared_agent_name` + `fixtures_dir`) and
per-scenario `instance` values that must be threaded consistently through every scenario.

The mandatory **`azd` binary build/verify gate** and **recipe validation** are shared gates the
**foundry-extension-scenario-orchestrator** owns (see the `foundry-extension-scenario-orchestrator` agent and
[`../foundry-extension-scenario-pr-regression/references/workflow.md`](../foundry-extension-scenario-pr-regression/references/workflow.md)
§ Step 1b). When the orchestrator dispatches this skill it has already run them; when this skill
runs standalone, perform those same gates first — do not run any scenario against an unverified
`azd` binary.

## Workflow

### Step 1 — Resolve the tag/tier filter

Translate the user's request into a `list_scenarios` filter:

- **Whole suite** ("run everything", "run all scenarios") → no tag filter (enumerate all), but
  still honor dependency ordering and the cost gate below.
- **By command** ("all `init` scenarios") → `tags=["cmd:init"]`.
- **By trait** ("the `parallel-safe` set", "smoke test") → the matching trait tag(s), e.g.
  `tags=["parallel-safe"]`.
- **By tier** ("all of Tier 0", "run Tier 2") → the tier tag(s), e.g. `tags=["tier:0"]`.

`list_scenarios` filtering is **OR across tags, case-sensitive, exact match**. See the scenarios
[README](../../../cli/azd/extensions/azure.ai.agents/tests/cli-interactive-tester-scenarios/README.md)
§ Tags for the taxonomy. If the request is ambiguous about scope (one command vs. a whole tier),
ask via `ask_user` before enumerating.

### Step 2 — Enumerate

```text
list_scenarios(root="<scenarios-dir>", tags=[<filter>, ...])   # omit tags for the whole suite
```

Build the concrete set before grouping or confirmation:

1. Inspect every selected scenario's `requires:` field. Recursively add each referenced
   prerequisite scenario, deduplicating paths, until the dependency graph is closed. A
   `verify-deploy` filter therefore adds the matching Tier 1 producers that create its
   scaffolds. If a referenced scenario does not exist, stop with a plan-construction error
   rather than scheduling a dependent that can only be skipped.
2. If the expanded set contains **any Tier 2 scenario**, add
   `tier2/2.00-setup-deploy-shared-agent.yaml` and
   `tier2/2.99-teardown-down.yaml` to the concrete set, deduplicating them if already present.
   This lifecycle closure is mandatory even when a tag filter such as `cmd:invoke` did not
   match the setup/teardown files directly.
3. Read and retain each selected scenario's optional top-level `produces` value so it can be
   passed directly to that scenario's worker. The tester's `load_scenario` summary does not
   expose orchestrator-only fields.

Group the dependency- and lifecycle-closed set by tier (0 / 1 / 1b / 2). The expanded grouping
drives the cost gate and plan display; dependencies and lane state drive execution readiness.

### Step 3 — Confirm the plan (cost gate)

Show the user the concrete scenario list grouped by tier and confirm via `ask_user` before
running:

- Always list the Tier 0 scenarios that will run (free).
- If the set includes **Tier 1**, confirm `az login` is done.
- If the set includes **Tier 1b** or **Tier 2**, require an **explicit cost acknowledgement**
  ("Tier 1b/2 provisions real Azure resources and incurs cost — proceed?"). If the user
  declines, drop the cost-incurring tiers and any prerequisite scenarios added solely for
  those dropped dependents; retain scenarios that matched the user's original filter.

Generate one `<run-id>` per the shared prerequisites and reuse it for the whole sweep. All
artifacts go under `<scenarios-dir>/.reports/<run-id>/`.

### Step 4 — Run the scenarios

Drive each selected scenario per the executor spec (see **Execution mechanics** below) — fan
each scenario out to a **foundry-extension-scenario-worker** agent, one scenario per worker,
using a readiness scheduler:

1. **Recipe validation (mandatory).** Run one fast Tier 0 scenario (e.g. `0.01-version`)
   synchronously before fanning out. If it fails with an infrastructure error, **stop the whole
   run** and fix the environment — do not fan out into a fleet of failures.
2. **Adaptive rolling pool.** Choose a safe target parallelism from the selected workload,
   available model/tool capacity, and Azure side effects. Launch ready Tier 0 / Tier 1 /
   Tier 1b scenarios up to that capacity. When any worker finishes, immediately process its
   result and launch enough ready work to refill available capacity; never wait for a batch.
   Give Tier 0 / Tier 1 workers the scenario-specific `instance` / `instance_id` derived in the
   prerequisites.
3. **Tier 1b** (`verify-deploy`, ⚠️ cost): a scenario becomes ready as soon as its own
   `requires:` prerequisite **PASSES** this run; it does not wait for unrelated Tier 1 workers.
   Reuse the prerequisite's exact instance ID. Its PASS must include a verified absolute
   `scaffold_dir`; add that exact value to the dependent's `session_vars` as
   `prerequisite_scaffold_dir`. Never reconstruct the directory from template, agent, or
   instance names. A producer failure or PASS without `scaffold_dir` skips only its dependent.
4. **Tier 2** (`serial-only`, ⚠️ cost): start this lane after recipe validation even while the
   rolling pool is active. Never run more than one Tier 2 scenario:
   `2.00-setup-deploy-shared-agent` **first**, then `2.01-`…`2.18-` **serially**,
   `2.18-delete` before teardown, and `2.99-teardown-down` **last**. Setup must PASS before
   functional scenarios run; otherwise skip them and proceed to cleanup/recovery. Each
   functional completion unlocks only the next selected Tier 2 scenario, and teardown follows
   the final attempted functional scenario regardless of verdict.

`requires:` gating is a run-level decision: before dispatching a scenario that declares
`requires:`, look up the prerequisite's verdict **in this run** and tell the worker whether it
passed. Collect each completion as it arrives, update the ready/running/blocked state, and
continue until every scenario has completed or been skipped.

### Step 5 — Report

Aggregate every worker's verdict into `.reports/<run-id>/FINAL-REPORT.md` (see
**Reporting** below). Post a PR comment **only** if the user explicitly asked to tie this sweep
to a PR; a suite run is not PR-scoped by default. If a Tier 2 run started but was interrupted
before `2.99-teardown`, run `2.99-teardown-down` (or `2.00-setup`'s down hook) so no Azure
resources are orphaned, then report that status.

## Execution mechanics (single source)

Per-scenario driving — path style, the `requires:` gate, the fail-loud execution rules,
parallelism / ordering, and capture — is **not** restated here. It lives once in the executor
spec:
[`driving-mechanics.md`](../../../cli/azd/extensions/azure.ai.agents/tests/cli-interactive-tester-scenarios/driving-mechanics.md)
(repo path
`cli/azd/extensions/azure.ai.agents/tests/cli-interactive-tester-scenarios/driving-mechanics.md`).
Fan each selected scenario out to a **foundry-extension-scenario-worker** agent, which loads, drives, and reports
one scenario per that spec. This skill's job is selection (tag/tier filter), gating, ordering,
and reporting — not driving.

## Reporting

The `FINAL-REPORT.md` format and the PASS / FAIL / ⏭️ SKIPPED rules are single-sourced in
[`../foundry-extension-scenario-pr-regression/references/reporting.md`](../foundry-extension-scenario-pr-regression/references/reporting.md)
(repo path `.github/skills/foundry-extension-scenario-pr-regression/references/reporting.md`). Follow it, with two
sweep-specific differences:

- **Header framing.** A sweep is not PR-scoped, so the run header records the **tag/tier filter**
  that selected the set (e.g. "Filter: `cmd:init` across all tiers", or "Full suite") instead of
  a PR number / impacted-tag set.
- **PR comment is opt-in.** By default write only the local artifact and print the summary to the
  user. Only post a PR comment if the user explicitly tied the sweep to a PR.

Never soften a real regression to make the table green.

## Exit criteria

- The tag/tier filter was resolved from the user's request (or confirmed as a whole-suite run),
  every selected scenario's `requires:` dependency graph was added to the concrete set, and
  that set was **confirmed by the user** — including an explicit cost acknowledgement before
  any Tier 1b or Tier 2 run.
- The shared gates (prerequisites, `azd` binary verify, recipe validation) passed before any
  fan-out.
- Every selected scenario was driven to a recorded PASS / FAIL / ⏭️ SKIPPED with duration and
  findings; scenarios with a `requires:` prerequisite that did not PASS are ⏭️ SKIPPED (not FAIL).
- A `FINAL-REPORT.md` was written under `.reports/<run-id>/`, and any Tier 1b / Tier 2 run
  was followed by appropriate teardown so no Azure resources are left running.
