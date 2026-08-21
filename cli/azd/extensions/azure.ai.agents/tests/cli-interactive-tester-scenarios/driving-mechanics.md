<!-- cspell:ignore defaultyourvalue nextstep -->
# Driving mechanics — the executor spec

**This is the single source of truth for *how* the cli-interactive-tester scenarios are
driven.** It is written for the **executor** — the `foundry-extension-scenario-worker` agent that drives one
scenario, and the run skills (`foundry-extension-scenario-pr-regression`, `foundry-extension-scenario-suite-run`) that select and
fan out scenarios. Agents and skills **link to this file** rather than restating it, so the
rules live in exactly one place.

- **Humans** author scenarios and read results — they don't perform these steps by hand. The
  human-facing **authoring contract** (what a passing scenario *means*, and what you owe a
  scenario as its author) lives in [`README.md`](./README.md). This doc is the operational
  counterpart: the runtime rules a driver must obey.
- If a rule here and the README's authoring contract ever seem to disagree, the README defines
  *intent* (how scenarios are judged) and this file defines *mechanics* (how to execute); they
  are designed to agree.

---

## Path style (Windows → WSL)

On Windows the tester drives CLIs through **tmux inside WSL**, and it resolves every
path-shaped MCP argument on the **WSL** side — not the orchestrator side. Pass POSIX paths:

| Orchestrator OS | Pass to MCP tools | Don't pass |
| --- | --- | --- |
| Windows | `/mnt/c/Repos/azure-dev/.../scenarios/tier0/0.01-version.yaml` | `C:\Repos\azure-dev\...\tier0\0.01-version.yaml` |
| macOS / Linux | native absolute path | — |

This applies to **every** path-shaped argument: `path:` on `load_scenario` / `run_pre_hooks` /
`run_post_hooks`, `scenario_path:` on `start_session`, and `output_dir:`.

**Failure-mode hint:** if `load_scenario` returns `Scenario file not found`, the path style is
almost certainly the cause — translate `C:\…` → `/mnt/c/…` and retry **one** call before
fanning out.

---

## Environment integrity (never work around a broken environment)

The driving agent must **never install, replace, or modify** the `azd` binary, any `azd`
extension, or any system tool during a run — on any OS. The environment is prepared **before**
scenarios start (the `azd` build/verify gate; see the orchestrator's prerequisites). If it is
broken, the run **stops** — the agent does not fix it.

- If a scenario fails due to an environment issue (wrong binary, missing tool, file-locking on
  WSL, path resolution failure, or similar), report it as **FAIL** with an infrastructure
  finding. Do **not** work around it by installing packages, switching binaries, downloading
  builds, or modifying system state.
- This applies to the orchestrator **and every fleet sub-agent**. No participant may alter the
  test environment.

---

## Per-scenario loop

For each selected scenario:

The orchestrator reads and retains the scenario YAML's optional top-level `produces` value
during plan construction and passes that raw value to the worker. `load_scenario` intentionally
does not expose unknown orchestrator-only fields.

0. **Check `requires:`** — if the scenario declares a `requires:` field (a relative path from
   the scenarios root, e.g. `tier1/1.01-init-template-python.yaml`), look up the
   prerequisite's result **in the current run**:
   - Prerequisite **PASSED** → if it returned `scaffold_dir`, add that exact absolute path to
     this scenario's `session_vars` as `prerequisite_scaffold_dir`, then proceed (step 1+).
   - Prerequisite **FAILED / not run / SKIPPED** → record this scenario as ⏭️ **SKIPPED** with
     reason `prerequisite <path> did not pass` and move on.
   - Prerequisite declares `produces` but its PASS result has no verified `scaffold_dir` →
     treat the producer result as invalid and SKIP this scenario. Never guess its output path.
1. `load_scenario(path=<wsl path>, session_vars=<per-scenario map>)` — also reports whether the
   scenario declares `pre` / `post` hooks. For parallel-safe scenarios the map includes the
   assigned `instance`.
2. If it has `pre` hooks:
   `run_pre_hooks(path=…, session_vars=…, instance_id=<assigned instance>)`. Hooks run
   host-side, sequentially, fail-fast (unless a hook sets `continue_on_error: true`).
3. `start_session(scenario_path=…, session_vars=…, instance_id=<assigned instance>,
   run_name=<scenario-stem>, output_dir=<wsl .reports path>)`.
4. Drive the scenario's `goals:` with `send_action` / `select` / best-effort screenshots.
5. If the worker received `produces` and its product goals succeeded, render that path with the
   same `session_vars`, resolve it to an absolute path, and verify through the tester session
   that it is a directory containing `azure.yaml` and `.azure/`. Retain the verified path as
   `scaffold_dir`. A missing or invalid scaffold fails the producer.
6. Whether the goals pass or fail, enter the finally-style cleanup path and call
   `finish_session` (this releases ports and generates the HTML report).
7. If it has `post` hooks, always call
   `run_post_hooks(path=…, session_vars=…, instance_id=<assigned instance>)` after
   `finish_session`, even when a product goal failed. A post-hook failure is a separate cleanup
   finding and makes the scenario FAIL. If product verification also failed, preserve both
   findings.

Use the same `instance` value in `session_vars` and `instance_id` on every tool that accepts
it. Tier 2 has no assigned instance and omits `instance_id`.

Always `finish_session` and attempt declared post hooks for every session you start.

---

## Execution rules (fail-loud)

These make the run actually *test* the CLI instead of papering over its bugs. They are the
operational form of the README's **authoring contract** — see
[`README.md`](./README.md) for what each means for goal-writing.

- **The scenario goals are the contract.** A scenario PASSES **only** when the product's actual
  behavior matches what the goals describe. If the goals say "expect error X" and the product
  prints a different (even reasonable) error, that is a **FAIL**. If the goals reference a flag
  or subcommand that no longer exists, that is a **FAIL**. Your job is to **verify** goals were
  met, not to **rationalize** why they weren't. Never mark a scenario PASSED with an
  "observation" when the goals were not achieved — observations are for incidental notes on
  scenarios that genuinely passed all goals.
- **Never adapt around broken goals.** If the goals instruct you to run a command/flag that
  doesn't exist, or expect output that doesn't appear, **FAIL** the scenario. Do not substitute
  an alternative command, skip the broken step, or invent a workaround — a human must update
  the scenario.
- **Never answer an unexpected product prompt.** Before responding, match the prompt to one
  explicitly described by the scenario goals (including a goal that marks it optional with
  wording such as "if asked"). If no goal permits it, capture and report the unexpected prompt,
  **FAIL** the scenario, and do not answer or continue the product flow. A vaguely related goal
  is not permission to proceed. Always finish the tester session and run any required cleanup or
  post hooks. Shell prompts and the interactive tester's own UI are not product prompts.
- **Never retry a failed scenario.** On failure (command error, unexpected output, non-zero
  exit) report the finding and move on. Do **not** re-run hoping for a different result unless
  the scenario's `goals:` explicitly instruct a retry — retrying masks flakiness.
- **Always run post-hook cleanup and report it independently.** Product failure stops further
  product driving, not cleanup. Finish the tester session, run every declared post hook, and
  record cleanup failure separately. A failed cleanup makes the scenario FAIL; when product
  verification also failed, report both rather than replacing the original finding.
- **Screenshot capture is non-blocking and is never retried.** A screenshot is supporting
  evidence, not product behavior. If a screenshot call errors or times out, immediately file a
  `report_finding` with category `observation`, including the capture error and which expected
  evidence is unavailable. Continue every remaining product-verification and cleanup step. If
  all product goals pass and capture is the only issue, return **PASS-with-finding**, not FAIL.
  A terminal/session failure that prevents observing or driving the product remains an
  infrastructure **FAIL**; this exception applies only to the screenshot operation.
- **Don't verify/retry after a `select`.** Reading back the echo and "correcting" a pick hides
  the very picker defect these runs exist to catch. Send the action; let downstream prompts
  surface any failure.
- **Treat a `select` miss as a hard failure.** The tester's `select` is fail-loud (a missing
  target surfaces as `ERROR during 'select': …`). Report a finding and **stop that scenario** —
  do not retry with a different `choice_text` / `choice_index`.
- **Prefer `choice_text` over `choice_index`** when the label is stable (indices shift between
  releases).
- **Clear a pre-filled text field before typing** (e.g. the agent-name prompt): select-all then
  delete/backspace first, otherwise your value *appends* to the default (`defaultyourvalue`).
  This applies only to text input, not select or multi-select prompts.
- **Translate multi-select goals according to intent.** Read the human-described final selection,
  identify whether the goal separately asserts the initial/default selection, and inspect the
  visible checkbox state before acting. If an asserted default does not match, report and
  **FAIL** without repairing or submitting the prompt. If the visible state already equals the
  requested final state, use the `key` action with `key: "Enter"` to continue without changing
  any option; do **not** call `multi_select`. Otherwise, when the goal permits changing the
  selection, call `multi_select` with only the indices whose checked state must change. Toggle
  indices are state changes, not the final selected set: never include an already-selected item
  merely because it should remain selected. Findings must describe the action payload actually
  recorded by the tester; never claim an unchanged submission when an option was toggled.
- **Pause before the first cloud-creating action.** Provisioning is expensive and
  irreversible-ish; the orchestrator's cost/consent gate must be satisfied before entering any
  `init` / `provision` flow that creates real resources (especially in parallel).

---

## Parallelism & ordering

Concurrency primitive: **parallel background sub-agents, one scenario per sub-agent.**

- **Validate the recipe with one scenario before fanning out.** Confirm `load_scenario` →
  `start_session` → one `send_action` round-trips for a single fast Tier 0 scenario (e.g.
  `0.01-version`). If it fails with an infrastructure error, **stop the whole run** and fix the
  environment — do not fan out into a fleet of failures.
- **Use an adaptive rolling pool.** The orchestrator chooses safe parallelism from the selected
  workload, available model/tool capacity, and Azure side effects; there is no fixed worker
  count. It launches ready Tier 0 / Tier 1 / Tier 1b workers up to that capacity. On every
  completion notification, it records the result, updates readiness, and immediately fills
  available capacity while ready work remains. It never waits for a batch of peers.
- **Tier 0 / Tier 1** (`parallel-safe`) are initially ready. Give each scenario a safe instance
  ID formed from its numeric scenario key and the sweep run ID (for example,
  `t104-0714103842-a1b2c3`) and include that identity in its descriptive `session_id`. Pass the
  instance both in `session_vars` and as `instance_id`; never let distinct scenarios fall back
  to `"main"`.
- **Tier 1b** (`parallel-safe`, `verify-deploy`, ⚠️ Azure cost) depends only on its declared
  Tier 1 producer. When that producer PASSES with a verified `scaffold_dir`, immediately add
  that exact path to the dependent's `session_vars` as `prerequisite_scaffold_dir` and make the
  dependent ready, reusing the producer's exact instance ID. A producer failure or invalid
  PASS skips only that dependent. Do not wait for unrelated Tier 1 scenarios and never
  reconstruct the scaffold path.
- **Tier 2** (`serial-only`, ⚠️ Azure cost) uses an independent serial lane that may overlap the
  rolling pool after recipe validation. Run `2.00-setup-deploy-shared-agent` **first**, then
  `2.01-`…`2.18-` **serially** (they share one deployed agent and mutate shared session/file/
  endpoint state), `2.18-delete` before teardown, then `2.99-teardown-down` **last**. Never run
  more than one Tier 2 worker. Setup must PASS before functional scenarios run; otherwise skip
  them and proceed to cleanup/recovery. Each functional completion makes only its successor
  ready, and teardown follows the final attempted functional scenario regardless of verdict.
  Tier 2 uses **no `instance_id`**.
- **Same scenario N times in parallel:** append a distinct ordinal to the scenario's normal
  run-scoped instance ID and use it for that copy's `session_vars.instance`, hooks, and every
  `start_session` call. Only Tier 0 work-dir scenarios and Tier 1 `init` scenarios support this.
  Tier 2 remains serial and never receives an instance ID; `2.12` prefixes its paired session
  IDs with `run-` / `invoke-` and includes `{run_id}` to isolate them from other sweeps.

### Choose safe parallelism

The wall-clock bottleneck is per-agent LLM time and per-account model concurrency, not the MCP
server (which is per-`session_id`-parallel by design). Choose enough concurrency to keep
available capacity busy without overwhelming the model, tester, or Azure account. Re-evaluate
the target as the ready workload changes; expensive Tier 1b work and an active Tier 2 lane may
justify lower overall concurrency than offline work. Background sub-agents are typically **not
cancellable mid-run** — launch cost-incurring work conservatively because a stop request cannot
recall an in-flight `azd provision`.

### Fleet sub-agent rules

Every spawned sub-agent must obey:

- **Do not modify the environment** (see [Environment integrity](#environment-integrity-never-work-around-a-broken-environment)).
- **Infrastructure errors → FAIL and return.** Fail the scenario with an infrastructure finding
  and return control to the orchestrator; do not attempt a fix.
- **Each sub-agent runs exactly one scenario** — loads it, drives the goals, reports
  PASS/FAIL/SKIP. It makes no decisions about other scenarios or the overall run.

---

## Artifacts & capture

- **`run_name=<scenario-stem>`** on every `start_session` (the YAML filename without `.yaml`,
  e.g. `0.01-version`, `2.02-show-json`). Without it the tester auto-names the run folder
  `agent_YYYYMMDD_HHMMSS`, which is hard to cross-reference. For scenarios that start two
  sessions (`2.12-run-local-and-invoke-local`), suffix a role tag (`…-run`, `…-invoke`).
- **`output_dir`** on every `start_session`, pointing at the WSL path of
  `<scenarios-dir>/.reports/<run-id>/tester-reports`. Generate **one** `<run-id>` per the shared
  prerequisites and **reuse it across every session**, so artifacts, worker sessions, and Azure
  resources from one run remain correlated and isolated.
- **Screenshot key steps on a best-effort basis** and file `report_finding` for any confusing
  UX, error, or doc mismatch. A screenshot error or timeout follows the non-blocking observation
  policy above and must identify the evidence that could not be captured.
- **Record per scenario** for the report: scenario stem, tier, **PASS / FAIL / ⏭️ SKIPPED**,
  wall-clock **duration** (`start_session` → `finish_session`, including hooks; formatted
  `Hh Mm Ss`, e.g. `3m 21s`, `1h 04m 12s`; `—` for SKIPPED), and any `report_finding` text.
  Include `scaffold_dir` for producers and `—` for scenarios that declare no output.
  SKIPPED scenarios include the reason (e.g. `prerequisite tier1/1.01-init-template-python.yaml
  did not pass`).
- The driving agent writes the final cross-scenario summary to
  `.reports/<run-id>/FINAL-REPORT.md` (the `.reports/` tree is git-ignored).
