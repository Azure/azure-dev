<!-- cspell:ignore defaultyourvalue -->
# Running scenarios through the tester

This mirrors the scenarios `README.md` ("Driving conventions" and "Orchestrating a fleet
run"). Follow it so the run actually *tests* the CLI instead of papering over its bugs.

## Path style (Windows → WSL)

On Windows the tester drives CLIs through tmux **inside WSL**, and it resolves every
path-shaped MCP argument on the WSL side. Pass POSIX paths:

| Orchestrator OS | Pass to MCP tools |
| --- | --- |
| Windows | `/mnt/c/Repos/azure-dev/.../scenarios/00-version.yaml` |
| macOS / Linux | native absolute path |

This applies to `path:` on `load_scenario` / `run_pre_hooks` / `run_post_hooks` and to
`scenario_path:` on `start_session`. If `load_scenario` returns `Scenario file not found`,
the path style is almost certainly the cause — translate `C:\…` → `/mnt/c/…` and retry once
before fanning out.

## Per-scenario loop

For each selected scenario:

1. `load_scenario(path=<wsl path>, session_vars=<merged profile>)` — also tells you whether
   the scenario declares `pre`/`post` hooks.
2. If it has `pre` hooks: `run_pre_hooks(path=…, session_vars=…)`. Hooks run host-side,
   sequentially, fail-fast (unless `continue_on_error: true`).
3. `start_session(scenario_path=…, session_vars=…, run_name=<scenario-stem>, output_dir=<wsl .reports path>)`.
   - `run_name` = the YAML filename without `.yaml` (e.g. `00-version`, `21-show-json`).
   - For scenarios that start two sessions (`27-run-local-and-invoke-local`), suffix the
     `run_name` with a role tag (`…-run`, `…-invoke`).
   - `output_dir` = WSL path of `<scenarios-dir>/.reports/<run-timestamp>/tester-reports`.
     Reuse the **same** `<run-timestamp>` across every scenario in the run.
4. Drive the session's `goals:` with `send_action` / `select_by_text` / screenshots, then
   `finish_session`.
5. If it has `post` hooks: `run_post_hooks(path=…, session_vars=…)`.

## Driving conventions (fail-loud)

- **The scenario goals are the contract.** A scenario PASSES only when the product's actual
  behavior matches what the goals describe. If the goals say "expect error X" and the
  product prints a different error (even a reasonable one), that is a FAIL. If the goals
  reference a flag or subcommand that no longer exists, that is a FAIL. The driving agent's
  job is to **verify** goals were met, not to **rationalize** why they weren't. Do not mark
  a scenario as PASSED with an "observation" when the goals were not achieved — observations
  are for incidental notes on scenarios that genuinely passed all their goals.
- **Don't verify/retry after a `select`.** Reading back the echo and "correcting" a pick
  hides the very picker defect the test exists to catch. Send the action and let downstream
  prompts surface any failure.
- **Treat a select miss as a hard failure.** `select_by_text` is fail-loud
  (`ERROR during 'select': …`). Report a finding and stop that scenario — do **not** retry
  with a different `choice_text`/`choice_index`.
- **Never retry a failed scenario.** If a scenario fails (command errors, unexpected output,
  non-zero exit), report the finding and move on. Do **not** re-run the scenario hoping for
  a different result — unless the scenario's `goals:` explicitly instruct a retry. Retrying
  masks flaky behavior and makes the test suite unreliable as a regression signal.
- **Never adapt around broken goals.** If the goals instruct you to run a command or flag
  that does not exist, or expect output that does not appear, fail the scenario. Do not
  substitute an alternative command, skip the broken step, or invent a workaround. The
  scenario must be updated by a human — the driving agent must not silently patch over it.
- **Prefer `choice_text` over `choice_index`** (indices shift between releases).
- **Clear a pre-filled text field before typing** (e.g. the agent-name prompt); otherwise
  your value *appends* to the default (`defaultyourvalue`).
- **Pause before the first cloud-creating action.** The Step 4 cost confirmation covers
  this; never enter a Tier 2 provision flow without it.

## Parallelism & ordering

- **Tier 0 / Tier 1** (`parallel-safe`): fan out in small waves (4–6 at a time), one
  sub-agent per scenario, each with a distinct descriptive `session_id` **postfixed with a
  timestamp** (e.g. `fleet-10-init-from-code-1752434100`). The timestamp (Unix epoch
  seconds) prevents collisions when multiple agent sessions drive the tester concurrently.
  No `instance_id` is needed — each scenario's `cwd` already isolates itself (defaults to
  the `-main` suffix).
- **Same scenario N times** in parallel: pass `instance_id="1"`, `"2"`, … See the README's
  parallel-readiness section for which scenarios support it.
- **Tier 2** (`serial-only`): never parallelize. Run `20-setup-deploy-shared-agent` first,
  then `21-…2A-` serially (they share one deployed agent and mutate shared session/file/
  endpoint state), then `2Z-teardown-down` last.
- **Validate the recipe with one scenario before fanning out** — confirm `load_scenario` →
  `start_session` → one `send_action` round-trips for a single Tier 0 scenario first.

## Capture per scenario

Record, for the report: the scenario stem, tier, PASS/FAIL, wall-clock **duration**
(`start_session` → `finish_session` incl. hooks, formatted `Hh Mm Ss`), and any
`report_finding` text (confusing UX, errors, doc mismatches).
