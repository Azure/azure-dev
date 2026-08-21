---
name: foundry-extension-scenario-authoring
license: MIT
metadata:
  version: "1.2"
  # Bump major on breaking prompt/trigger changes; bump minor on new references or authoring rules.
  # 1.0: initial authoring + validation skill for the azure.ai.agents cli-interactive-tester
  # scenarios. Taxonomy (tiers/tags/profile/hooks/fixtures/requires) is single-sourced in the
  # scenarios README; this skill adds the authoring procedure and a no-execution validation loop.
  # 1.1: require deterministic input for every executed invoke command.
  # 1.2: document the run-scoped identity placeholders supplied by orchestration.
description: >-
  **WORKFLOW SKILL** — Authors and validates cli-interactive-tester **scenarios** for the
  azure.ai.agents extension: writes a new goal-based scenario YAML (or edits an existing one) so
  it follows the framework's tier / tag / hook / fixtures / requires conventions and the goals-
  are-the-contract judging rules, then lint-validates it **without running it** (no Azure cost).
  Typically driven through the foundry-extension-scenario-author agent.

  INVOKES: cli-interactive-tester MCP tool list_scenarios (for tag/lint validation only — never
  start_session), read/edit of scenario YAML files, ask_user.

  USE FOR: write a new scenario, add scenario coverage for a command or flag, author a
  cli-interactive-tester scenario, fix a scenario's tags / hooks / requires, add a fixture for a
  scenario, bring a scenario up to the authoring contract, close a coverage gap flagged by a PR
  regression run.

  DO NOT USE FOR: RUNNING scenarios or a suite (use foundry-extension-scenario-suite-run, or foundry-extension-scenario-pr-regression
  for a PR — this skill never drives a scenario or incurs Azure cost), driving a single scenario
  (that is the foundry-extension-scenario-worker agent), azd core preflight (use azd-preflight), changelog (use
  changelog-generation), creating PRs (use pull-request), scenarios for any extension other than
  azure.ai.agents.
---

# foundry-extension-scenario-authoring

Authors and validates goal-based **scenarios** for the `azure.ai.agents`
[cli-interactive-tester](https://github.com/coreai-microsoft/cli-interactive-tester) suite under
`cli/azd/extensions/azure.ai.agents/tests/cli-interactive-tester-scenarios/`. This skill
**writes and lints** scenarios; it never **runs** them (running is `foundry-extension-scenario-suite-run` /
`foundry-extension-scenario-pr-regression`, driven by the `foundry-extension-scenario-worker` agent, and incurs Azure cost for
Tier 1b / Tier 2).

## The authoring contract (read this first)

A scenario's `goals:` are its **contract**: a run PASSES only when the product's real behavior
matches what the goals literally describe. So the whole point of authoring is to write goals
that are a *verifiable spec of correct behavior* — not a loose description the driver has to
interpret charitably. The judging rules are single-sourced in the scenarios README:
[**How scenarios are judged (authoring contract)**](../../../cli/azd/extensions/azure.ai.agents/tests/cli-interactive-tester-scenarios/README.md#how-scenarios-are-judged-authoring-contract).
The consequences for how you write goals:

- **Write goals as literal, checkable assertions.** "Confirm the process exits cleanly and
  prints a version string" is checkable; "make sure version works" is not. A driver will FAIL a
  scenario whose goals aren't met — it will not rationalize a near-miss into a pass.
- **Never encode a workaround in a goal.** If a goal names a flag/command that doesn't exist or
  expects output that never appears, the scenario FAILS by design. Keep goals current with the
  product; don't paper over a bug in the goal text.
- **Key interactive pickers off stable text labels, not positions.** The driver prefers
  `choice_text` over `choice_index`, so phrase picker goals around the label (e.g. *select "Use
  the code in the current directory"*) — indices shift between releases.
- **Enumerate expected product prompts.** Goals define every product prompt allowed during the
  scenario. Name each prompt with stable user-visible text and state the response. Use "if
  asked" only for a genuinely optional prompt. Never write "follow the prompts" or another
  catch-all; an unlisted product prompt must fail the scenario.
- **Give every executed invoke explicit input.** Write the literal command with a quoted
  positional message. Use `--input-file` only when file input is what the scenario tests, and
  define the file contents first. Never leave command construction to the worker with wording
  such as "run invoke and send a simple message"; bare invoke does not prompt for a message.
- **Guard pre-filled prompts.** When a prompt comes pre-populated (e.g. the agent name), the
  goal must tell the driver to **clear the field first** before typing, or the typed value
  appends to the default. See the RESOURCE NAMING / AGENT NAME goals in
  `tier1/1.04-init-from-code.yaml` for the canonical phrasing.
- **Gate cost explicitly.** Any goal that reaches `azd provision` / real resource creation
  belongs in Tier 1b or Tier 2 and must be tagged and tiered accordingly (see below).

> The deeper *why* behind the picker / clear-field / no-retry rules is the executor spec
> [`driving-mechanics.md`](../../../cli/azd/extensions/azure.ai.agents/tests/cli-interactive-tester-scenarios/driving-mechanics.md).
> You don't need to restate it — just author goals that hold under it.

## Scenario anatomy

One file targets **one** command or flow. The taxonomy behind each field is single-sourced in
the README — this is the annotated skeleton; follow the links for the rules:

```yaml
# One-line comment: tier, what it targets, and any cost/prerequisite note.
name: "short-kebab-name"                 # tester run label; keep it short and unique-ish
command: "azd ai agent <subcommand> …"   # the installed extension entry point
cwd: "~/working/azd-agents-<slug>-{instance}"   # see cwd + idempotency below; /tmp for read-only
tags: ["tier:N", "cmd:<name>", "parallel-safe"] # REQUIRED — see the Tags taxonomy
requires: "tier1/1.0X-….yaml"            # optional; only when this scenario needs another to PASS first
produces: "~/working/azd-agents-…-{instance}/generated-agent" # optional producer output
env:                                      # optional; init scenarios disable agent auto-detect
  AZD_DISABLE_AGENT_DETECT: "1"
allocate_ports: [agent]                   # optional; only for scenarios that bind a port (e.g. 2.12)
pre:                                      # optional host-side setup (reset dir, seed fixture, auth guard)
  - run: "rm -rf {cwd-or-path}"
    cwd: "~/working"
    name: "reset working dir"
post:                                     # optional host-side cleanup
  - run: "…"
    name: "cleanup"
goals:                                    # product behavior is the literal, checkable contract
  - "Wait for … and confirm …."
  - "Take a screenshot of the final output." # best-effort evidence; capture failure is an observation
  - "Report a finding if …."
```

Field references (do not restate these — link to them):

- **Tiers & placement** — which directory (`tier0/`…`tier2/`) and cost profile:
  [README § Tiers](../../../cli/azd/extensions/azure.ai.agents/tests/cli-interactive-tester-scenarios/README.md#tiers).
- **Tags** — the `tier:N` / `cmd:*` / trait namespaces and rules:
  [README § Tags](../../../cli/azd/extensions/azure.ai.agents/tests/cli-interactive-tester-scenarios/README.md#tags).
- **`requires:`** — cross-scenario prerequisites (Tier 1b → Tier 1):
  [README § The `requires:` field](../../../cli/azd/extensions/azure.ai.agents/tests/cli-interactive-tester-scenarios/README.md#the-requires-field).
- **`produces:`** — the verified Tier 1 scaffold handed to Tier 1b:
  [README § Producer/consumer scaffold handoff](../../../cli/azd/extensions/azure.ai.agents/tests/cli-interactive-tester-scenarios/README.md#producerconsumer-scaffold-handoff).
- **Profile/session placeholders** (`{prefix}`, `{subscription}`, `{region}`, `{model}`,
  `{tenant}`, `{run_id}`, `{shared_agent_name}`, `{fixtures_dir}`, `{instance}`):
  [README § Profile / overrides](../../../cli/azd/extensions/azure.ai.agents/tests/cli-interactive-tester-scenarios/README.md#profile--overrides).
- **Pre/post hooks** — semantics (host-side, sequential, fail-fast), fields, and the reset /
  fixture-seed / auth-guard patterns:
  [README § Pre/post hooks](../../../cli/azd/extensions/azure.ai.agents/tests/cli-interactive-tester-scenarios/README.md#prepost-hooks).
- **Fixtures** — the `{fixtures_dir}` seed pattern:
  [README § Fixtures](../../../cli/azd/extensions/azure.ai.agents/tests/cli-interactive-tester-scenarios/README.md#fixtures).
- **Idempotency** — how a stateful scenario resets itself so re-runs start clean:
  [README § Re-running scenarios](../../../cli/azd/extensions/azure.ai.agents/tests/cli-interactive-tester-scenarios/README.md#re-running-scenarios-idempotency).
- **Conventions & parallel-readiness** — resource naming, `{instance}` suffixing, port allocation:
  [README § Conventions](../../../cli/azd/extensions/azure.ai.agents/tests/cli-interactive-tester-scenarios/README.md#conventions)
  and [§ Parallel-readiness](../../../cli/azd/extensions/azure.ai.agents/tests/cli-interactive-tester-scenarios/README.md#parallel-readiness--port-allocation).

## Authoring procedure

1. **Confirm the target and scope.** Identify the single command / flow under test and whether
   it is a happy path or a `negative-path` (error / non-zero-exit) assertion. One command per
   file — if you're tempted to test two, that's two scenarios (or a documented lifecycle
   scenario, like `2.07-sessions-lifecycle`). If the request is broad or ambiguous, ask via
   `ask_user` which command and tier are intended.

2. **Pick the tier and file placement.** Choose the lowest tier that can actually exercise the
   behavior (offline Tier 0 > auth-only Tier 1 > deploy-verify Tier 1b > cloud Tier 2), because
   cost and flakiness rise with tier. Place the file in the matching `tierN/` directory and name
   it `<tier-number>.<seq>-<slug>.yaml`, continuing the existing numbering in that directory.

3. **Write `command` and `cwd`.**
   - `command:` invokes the installed extension (`azd ai agent …`) or `azd …` for
     provision/deploy/down steps.
   - Read-only, stateless scenarios (`version`, `--help`, `sample list`) use `cwd: "/tmp"` and
     declare no hooks.
   - Stateful scenarios use a dedicated working dir suffixed with `-{instance}` (e.g.
     `~/working/azd-agents-<slug>-{instance}`) so parallel instances stay isolated.

4. **Make it idempotent with hooks.** For a stateful scenario add a `pre` hook that `rm -rf`s
   its own `cwd` (and, if it needs source code, a second hook that seeds a fixture from
   `{fixtures_dir}` — see `tier1/1.04-init-from-code.yaml`). Prefer **pre-wipe only** for
   local-only scenarios so the scaffold stays on disk for inspection. Tier 1b scenarios that
   provision paid resources require an always-run `post` hook with a suitable timeout for
   `azd down --force --purge`. Tier 2 setup additionally downs a project at its current-run
   path before removing that directory; teardown failure must abort setup and preserve state
   for recovery. Do not use `continue_on_error` when cleanup failure would orphan resources.

5. **Add a fixture only if required.** If the flow needs pre-existing source (the "use the code
   in the current directory" path), add the minimal tree under `fixtures/<name>/` and seed it via
   the hook. Keep fixtures minimal, but make them runnable when a later scenario deploys and
   invokes the generated scaffold; detection-only placeholders are not sufficient for
   deploy-verification prerequisites.

6. **Declare producer/consumer handoff when required.** A Tier 1 scenario whose scaffold is
   deployed by Tier 1b sets `produces:` to the exact generated project directory. Its Tier 1b
   consumer points `requires:` at that Tier 1 scenario (a **relative path from the scenarios
   root**) and uses `{prerequisite_scaffold_dir}` for `cwd` and every scaffold hook path. Never
   duplicate the producer's path in the consumer. Don't use `requires:` to sequence independent
   scenarios — it is a hard prerequisite, not an ordering hint.

7. **Write the `tags:` list.** At minimum: one `tier:N`, at least one `cmd:*`, and exactly one of
   `parallel-safe` / `serial-only` (Tier 0/1/1b → `parallel-safe`; Tier 2 → `serial-only`). Add
   `negative-path`, `picker`, or `verify-deploy` when they apply. Multi-command flows carry
   multiple `cmd:*` tags.

8. **Write the `goals:` as the contract.** Follow the authoring-contract rules above: literal,
   checkable product behavior, stable-label picks, clear-field-before-typing, best-effort
   screenshot evidence, and a final "report a finding if …" goal. Screenshot errors and timeouts
   are observations and do not block later product goals; all product expectations remain strict.
   For multi-select prompts, choose one explicit intent:
   - A **default assertion** lists what must already be selected and unselected, then says to
     leave that selection unchanged and continue. A mismatch must fail without repair.
   - A **final-state instruction** lists what should end selected and unselected without
     asserting the initial state, allowing the worker to change only the differences.
   An already-correct selection advances without changing any choice. Keep action names, indices,
   payloads, and keystrokes out of scenario goals; the worker owns those mechanics.
   For resource-creating flows, include the RESOURCE NAMING and AGENT NAME goals (prefix
   `{prefix}-`, suffix `-{instance}`) so parallel runs don't collide.

## Validation loop (no execution)

Validate **statically** — never `start_session`, never drive the scenario, never provision.

1. **Tag lint via `list_scenarios`.** Run
   `list_scenarios(root="<scenarios-dir>", tags=[<the new scenario's tags>])` and confirm the new
   file appears under each of its tags. If `list_scenarios` reports `tags: []` for the file, the
   `tags:` field is missing or malformed — fix it. (`list_scenarios` uses a real YAML parser, so
   this also catches YAML syntax errors in the file.)
2. **YAML shape.** Re-read the file and confirm required fields (`name`, `command`, `cwd`,
   `tags`, `goals`) are present and well-formed, that `goals` is a non-empty list of strings,
   and that any Tier 1 producer declares the exact scaffold directory in `produces:`.
3. **`requires:` resolves.** If `requires:` is set, confirm the referenced path exists relative
   to the scenarios root and points at the intended prerequisite.
4. **Fixture / hook paths resolve.** If a `pre` hook seeds a fixture, confirm the referenced
   `fixtures/<name>/` tree exists and the hook uses `{fixtures_dir}` (not a hardcoded path).
5. **Placeholders only reference known variables.** Every `{name}`-shaped token in `command` /
   `cwd` / hooks / goals is processed as a placeholder and must be a profile placeholder or
   `{instance}` or, for Tier 1b, `{prerequisite_scaffold_dir}`. This includes embedded shell
   syntax: format an awk action as `{ print }`, not `{print}`, so it cannot be mistaken for an
   unknown placeholder. Reject every unknown brace-delimited token during static validation.
6. **Invoke input is explicit.** For every goal or `command` that executes
   `azd ai agent invoke`, confirm the literal command includes a positional message or an
   intentional `--input-file` whose contents are defined by the scenario. Command-list and help
   assertions that only mention `invoke` are not executable calls.
7. **Coverage cross-check.** If this scenario closes a coverage gap flagged by a PR regression
   run, confirm its `cmd:*` tag matches the changed command so future impact-mapping picks it up.

Report the validation result to the user. If they then want to *run* the new scenario to confirm
it drives cleanly, that's a separate, cost-gated step — hand off to the `foundry-extension-scenario-suite-run`
skill / `foundry-extension-scenario-orchestrator` agent (Tier 1b / Tier 2 incur Azure cost and need explicit
consent). This skill stops at a validated, unexecuted scenario.

## Exit criteria

- A single-command scenario YAML was authored (or corrected) in the correct `tierN/` directory
  with a compliant `tags:` list, appropriate `cwd` + idempotency hooks, any needed fixture /
  `requires:`, and `goals:` written as a literal, checkable contract.
- The static validation loop passed: `list_scenarios` lists the file under all its tags (not
  `tags: []`), the YAML parses, and every `requires:` / fixture / placeholder reference resolves.
- **No scenario was executed** and no Azure resources were created; any run-to-confirm was handed
  off to the run skills with a cost note.
