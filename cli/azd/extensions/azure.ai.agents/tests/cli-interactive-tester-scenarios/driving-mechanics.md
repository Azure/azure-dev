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

0. **Check `requires:`** — if the scenario declares a `requires:` field (a relative path from
   the scenarios root, e.g. `tier1/1.01-init-template-python.yaml`), look up the
   prerequisite's result **in the current run**:
   - Prerequisite **PASSED** → proceed (step 1+).
   - Prerequisite **FAILED / not run / SKIPPED** → record this scenario as ⏭️ **SKIPPED** with
     reason `prerequisite <path> did not pass` and move on.
1. `load_scenario(path=<wsl path>, session_vars=<merged profile>)` — also reports whether the
   scenario declares `pre` / `post` hooks.
2. If it has `pre` hooks: `run_pre_hooks(path=…, session_vars=…)`. Hooks run host-side,
   sequentially, fail-fast (unless a hook sets `continue_on_error: true`).
3. `start_session(scenario_path=…, session_vars=…, run_name=<scenario-stem>, output_dir=<wsl .reports path>)`.
4. Drive the scenario's `goals:` with `send_action` / `select` / best-effort screenshots.
   Whether the goals pass or fail, enter the finally-style cleanup path and call
   `finish_session` (this releases ports and generates the HTML report).
5. If it has `post` hooks, always call
   `run_post_hooks(path=…, session_vars=…)` after `finish_session`, even when a product goal
   failed. A post-hook failure is a separate cleanup finding and makes the scenario FAIL. If
   product verification also failed, preserve both findings.

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
- **Tier 0 / Tier 1** (`parallel-safe`): fan out in **small waves (4–6 at a time)**, rolling
  forward. Give each sub-agent a distinct, descriptive `session_id` **suffixed with a Unix-epoch
  timestamp** (e.g. `fleet-1.04-init-from-code-1752434100`) to avoid collisions when multiple
  agent sessions drive the tester concurrently. **No `instance_id` needed** — each scenario's
  `cwd` already isolates itself via `{instance}`, which defaults to `"main"`.
- **Tier 1b** (`parallel-safe`, `verify-deploy`, ⚠️ Azure cost): runs **after all Tier 1
  scenarios complete**. Each declares a `requires:` field pointing at the Tier 1 scaffold it
  deploys — only run it if that prerequisite **PASSED**; otherwise ⏭️ SKIP. Once prerequisites
  are confirmed, fan out Tier 1b concurrently (independent Azure environments). Needs the same
  cost acknowledgement as Tier 2.
- **Tier 2** (`serial-only`, ⚠️ Azure cost): **never parallelize.** Run
  `2.00-setup-deploy-shared-agent` **first**, then `2.01-`…`2.18-` **serially** (they share one
  deployed agent and mutate shared session/file/endpoint state), `2.18-delete` before teardown,
  then `2.99-teardown-down` **last**. Tier 2 uses **no `instance_id`** (it would break the
  shared-agent assumption).
- **Same scenario N times in parallel:** pass `instance_id="1"`, `"2"`, … per `start_session`
  call; reuse the same `instance_id` for paired `run`/`invoke` sessions of one scenario. Only
  scenarios authored for it support this (Tier 0 work-dir scenarios, all Tier 1 `init`
  scenarios, and `2.12` for its allocated `{agent}` port).

### Keep waves small

The wall-clock bottleneck is per-agent LLM time and per-account model concurrency, not the MCP
server (which is per-`session_id`-parallel by design). Launching 4–6 sub-agents at a time and
rolling forward typically finishes a sweep faster than launching everything at once. Background
sub-agents are typically **not cancellable mid-run** — for Tier 1b/Tier 2 Azure side effects,
launch conservatively (a stop request can't recall an in-flight `azd provision`).

### Fleet sub-agent rules

Every sub-agent spawned for a wave must obey:

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
  `<scenarios-dir>/.reports/<run-timestamp>/tester-reports`. Pick **one** `<run-timestamp>`
  (form `YYYYMMDD-HHMMSS`) per suite run and **reuse it across every session**, so all scenarios
  from one run group under a single folder.
- **Screenshot key steps on a best-effort basis** and file `report_finding` for any confusing
  UX, error, or doc mismatch. A screenshot error or timeout follows the non-blocking observation
  policy above and must identify the evidence that could not be captured.
- **Record per scenario** for the report: scenario stem, tier, **PASS / FAIL / ⏭️ SKIPPED**,
  wall-clock **duration** (`start_session` → `finish_session`, including hooks; formatted
  `Hh Mm Ss`, e.g. `3m 21s`, `1h 04m 12s`; `—` for SKIPPED), and any `report_finding` text.
  SKIPPED scenarios include the reason (e.g. `prerequisite tier1/1.01-init-template-python.yaml
  did not pass`).
- The driving agent writes the final cross-scenario summary to
  `.reports/<run-timestamp>/FINAL-REPORT.md` (the `.reports/` tree is git-ignored).
