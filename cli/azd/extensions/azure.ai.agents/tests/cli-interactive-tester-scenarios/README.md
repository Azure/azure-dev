<!-- cspell:ignore benhanrahan -->
# `azd ai agent` — cli-interactive-tester scenarios

Goal-based scenarios for driving the `azure.ai.agents` extension through the
[cli-interactive-tester](https://github.com/coreai-microsoft/cli-interactive-tester)
MCP server. Each file targets **one** command or flow at a time and uses the
strict `goals:` list format so the run is repeatable and reviewable.

## How to run

Register the cli-interactive-tester MCP server (see its README), then
**bootstrap your profile** (one-time, per checkout — see [Profile / overrides](#profile--overrides)):

```sh
cd cli/azd/extensions/azure.ai.agents/tests/cli-interactive-tester-scenarios
cp profile.local.yaml.example profile.local.yaml
# edit profile.local.yaml — set `prefix` and `subscription` at minimum
```

Then ask Copilot CLI to load a scenario and accomplish its goals. The
orchestrator must **load both profile files, merge them (local overrides
shared), derive `shared_agent_name = {prefix}-{shared_agent_suffix}`, and pass
the merged map as `session_vars` on every `load_scenario`, `run_pre_hooks`,
`start_session`, and `run_post_hooks` call** — the scenario YAMLs reference
those values via `{prefix}`, `{subscription}`, `{region}`, `{model}`,
`{tenant}` (optional), and `{shared_agent_name}` placeholders.

Most scenarios here declare **`pre:` hooks** (host-side setup such as resetting
the working dir or seeding a fixture), and a few declare **`post:` hooks**
(cleanup). The agent must invoke them via the tester's `run_pre_hooks` /
`run_post_hooks` MCP tools — `load_scenario` surfaces whether a scenario has any.
See [Pre/post hooks](#prepost-hooks) below.

Here's also a sample prompt to run all of the scenarios, utilizing fleet mode:

```
Within the agents extension, there is a tests/cli-interactive-tester-scenarios directory, containing
a set of test scenarios for the cli-interactive-tester. I want you to use the cli-interactive-tester to;
    load the scenarios,
    start the session and accomplish the goals,
    if the scenario declares pre or post hooks, run them before/after the session,
    and take screenshots at each step.

First, read tests/cli-interactive-tester-scenarios/profile.yaml and profile.local.yaml and merge
them (local overrides shared); also derive shared_agent_name = "{prefix}-{shared_agent_suffix}".
Pass the merged map as session_vars on every load_scenario / run_pre_hooks / start_session /
run_post_hooks call — the scenarios reference {prefix}, {subscription}, {region}, {model},
{tenant} (optional), and {shared_agent_name} placeholders.

I want this run on fleet mode, to parallelize the tests as much as possible. Each of the scenarios
in tiers 0 and 1 are completely independent of each other and can be run in parallel. The scenarios
in tier 2 however rely on a setup scenario, and the teardown scenario should be run last, so make
sure to take that into account when distributing the work. I want to run all of the tests regardless
of tier, and I acknowledge that tier 2 has an azure cost implication, that's fine.

After all of these scenarios are run, create a final result report.

Create a plan to accomplish this
```

For more selective fan-outs (e.g. "just the `init` scenarios" or "everything
in Tier 0") the tester's `list_scenarios` MCP tool filters by `tags:`. See
[Tags](#tags) below for the taxonomy and an example tag-filtered prompt.

## Paths run inside WSL (on Windows)

The cli-interactive-tester drives CLIs through **tmux**, which on Windows runs
inside **WSL**. The scenario YAML files live on the Windows filesystem (in this
repo), but every `cwd` value is resolved against the **WSL filesystem** where the
command actually executes:

- `~/working/azd-agents-shared` → `/home/<wsl-user>/working/azd-agents-shared`
- `/tmp` → WSL's `/tmp`

Implications:

- `azd` and the `azure.ai.agents` extension must be installed **inside WSL**,
  since that is where the scenario commands run.
- `cwd` directories do not need to pre-exist — the tester creates them if missing.
- The `cwd` convention is three-way by design: ephemeral `/tmp` for read-only
  scenarios that touch no project (`version`, `--help`, `sample list`); a unique
  `~/working/azd-agents-*-{instance}` dir per `init`/`doctor` scenario for
  isolation (the `{instance}` suffix keeps concurrent runs of the same scenario
  apart — see [Parallel-readiness](#parallel-readiness--port-allocation)); and a
  single shared `~/working/azd-agents-shared` dir for all Tier 2 scenarios so they
  operate on the same deployed agent. `20-setup` runs `init` in that shared dir,
  which scaffolds the project into a subdirectory named after the agent, so the
  deployed project actually lives in `~/working/azd-agents-shared/{shared_agent_name}`
  (where `{shared_agent_name} = {prefix}-{shared_agent_suffix}` from your
  [profile](#profile--overrides), e.g. `alice-basic-responses`); the reuse and
  teardown scenarios run with that subdirectory as their `cwd`.

On macOS/Linux these are simply native paths (no WSL involved).

### This applies to MCP tool arguments too

The same path-resolution rule applies to **every path-shaped argument an
orchestrator passes to the tester's MCP tools** — most importantly the `path:`
argument on `load_scenario`, `run_pre_hooks`, and `run_post_hooks`, and the
`scenario_path:` argument on `start_session`. The server resolves them on the
WSL side, **not** on the orchestrator side. On Windows hosts, pass a POSIX path:

| Orchestrator OS | Pass to MCP tools | Don't pass |
| --- | --- | --- |
| Windows | `/mnt/c/Repos/azure-dev/cli/azd/extensions/azure.ai.agents/tests/cli-interactive-tester-scenarios/00-version.yaml` | `C:\Repos\azure-dev\...\00-version.yaml` |
| macOS / Linux | native absolute path | — |

**Failure-mode hint:** if `load_scenario` returns `Scenario file not found`, the
path style is almost certainly the cause — translate `C:\…` to `/mnt/c/…` and
retry one call before fanning out.

### Installing azd in WSL (Windows developers)

The scenarios must run **native Linux binaries** inside WSL. Symlinking to
`azd.exe` on the Windows side does not work — it causes `git safe.directory`
errors, TTY detection failures, and file locking issues.

To build and install your local dev code as native Linux binaries in WSL:

```bash
# From inside WSL:
cd /mnt/c/Repos/azure-dev/cli/azd/extensions/azure.ai.agents/tests/cli-interactive-tester-scenarios
bash setup-wsl.sh
```

This script:
1. Cross-compiles `azd` core (`linux/amd64`) → `/usr/local/bin/azd`
2. Cross-compiles the extension (`linux/amd64`) → `~/.azd/extensions/azure.ai.agents/`
3. Prints version confirmation

**Re-run `setup-wsl.sh` after every local code change** you want to test.
Requires the Go toolchain installed in WSL.

## Authentication

Tier 1 and Tier 2 scenarios read from / write to Azure, so a **human must log in
manually before** starting a run. The scenarios do **not** perform login
themselves, and the test-driving agent **cannot** complete it either: `az login`
opens a **separate browser window** for account selection that requires
human interaction outside the terminal the agent controls. Treat auth as a
one-time manual prerequisite, not a scenario step.

Inside WSL, a human runs (substituting `{tenant}` and `{subscription}` with
the values from their [profile](#profile--overrides) — omit `--tenant`
entirely if `tenant` isn't set in `profile.local.yaml`):

```
az login --tenant {tenant}      # or just `az login` if {tenant} is unset
```

This opens the interactive sign-in flow and then:

1. **Browser account selection** — a separate browser window opens; the human
   picks the account in the `{tenant}` tenant (or any tenant, if `{tenant}`
   isn't set). The agent cannot do this.
2. **Subscription selection** — back in the terminal, select the
   `{subscription}` subscription.

Tier 0 (`00-`) scenarios need no auth. Run this `az login` step once per WSL
session **before** asking the agent to drive any Tier 1/Tier 2 scenario; all of
them reuse that session credential.

### GitHub login (manifest scenarios)

The manifest scenarios (`10-init-from-manifest-url`,
`10-init-flags-agent-name-model`) download an agent manifest — and its sibling
files — from a public GitHub repo. The CLI first tries the anonymous GitHub API,
but when that's rate-limited (60 req/hr) it falls back to the `gh` CLI, which
would otherwise drop into an **interactive GitHub login** mid-run. Like
`az login`, this is a one-time manual prerequisite the agent can't complete, so a
human must run it once per WSL session **before** driving those scenarios:

```
gh auth login
```

Those scenarios include a `pre` hook that runs `gh auth status` and **fails fast**
if GitHub CLI isn't authenticated, so a missing login surfaces as a clear setup
error instead of a hung interactive prompt.

## Parallel-readiness & port allocation

The tester can run **N concurrent instances of the same scenario** and can
**allocate free TCP ports** per run. Scenarios here are authored to take
advantage of both where it's safe.

- **`{instance}` substitution.** `start_session(..., instance_id="1")` exposes
  `{instance}` for substitution into `command`, `cwd`, `env`, hook fields, and
  `goals`. It **defaults to `"main"`** when `instance_id` is omitted, so a single
  run is unchanged (dirs/names just end in `-main`).
- **Which scenarios are parallel-ready:**
  - **Tier 0 work-dir scenarios** (`doctor`, picker, validate) and **all Tier 1
    `init` scenarios** suffix their `cwd` (and hook paths) with `-{instance}`, so
    concurrent instances get isolated working directories.
  - **Tier 1 resource names** are suffixed with `-{instance}` too (via the
    RESOURCE NAMING goal and the `--agent-name` flag), so parallel instances
    don't collide on Azure resource names.
  - **`27-run-local-and-invoke-local`** declares `allocate_ports: [agent]` and
    binds `azd ai agent run`/`invoke --local` to `--port {agent}`. A port pool is
    shared across every `start_session` with the same `scenario_path`, so the
    `run` and `invoke` sessions find each other; parallel local runs each get a
    distinct port instead of colliding on the default `8088`.
- **Single-instance by design:** the **Tier 2 reuse scenarios** (`21-`…`2A-`),
  plus `20-setup` and `2Z-teardown`, all share the one deployed agent under
  `~/working/azd-agents-shared` (the project itself lives in the
  `{shared_agent_name}` subdirectory created by `20-setup`). They are
  **not** parameterized with `{instance}` (doing so would break the shared-agent
  assumption) and should be run serially.

To fan out, pass a distinct `instance_id` per `start_session` call (and reuse the
same `instance_id` for paired `run`/`invoke` sessions of one scenario).

## Orchestrating a fleet run

When a driving agent wants to run **many scenarios concurrently** (e.g. via
parallel background sub-agents, one scenario per sub-agent), pick the right
fan-out primitive for the shape of the run:

- **Different scenarios in parallel** (the common case for a full Tier 0/1
  sweep): give each sub-agent a distinct, descriptive `session_id` — e.g.
  `fleet-00-version`, `fleet-10-init-from-code` — and call `start_session` with
  the scenario's own `cwd`. **No `instance_id` is needed**: each scenario's `cwd`
  already isolates itself via the `{instance}` substitution, which defaults to
  `"main"` when `instance_id` is omitted.
- **Same scenario N times in parallel:** use `instance_id="1"`, `"2"`, … per
  call. See [Parallel-readiness](#parallel-readiness--port-allocation) for which
  scenarios are authored to support this.
- **Tier 2 ordering is fixed**, not parallel-friendly. Run `20-setup` first,
  then the targeted `21-…2A-` scenarios **serially** (they share one deployed
  agent and mutate shared state — sessions, files, endpoint configuration —
  so parallel runs interfere), then `2Z-teardown` last. See the
  [Tier 2](#tier-2--cloud-end-to-end-prefix-2x---%EF%B8%8F-incurs-azure-cost)
  section.

### Operational guardrails for the orchestrator

A few hard-won lessons that apply regardless of fleet size:

- **Validate the recipe with one sub-agent before fanning out.** Spend 30
  seconds confirming that `load_scenario`, `start_session`, and one
  `send_action` round-trip work end-to-end for *one* scenario before launching
  a wave. This is the cheapest way to catch wiring issues (wrong path style,
  wrong tool surface, auth not set up) before they multiply across many agents.
- **Background sub-agents are typically not cancellable mid-run.** Once launched,
  they will run to completion (or until the runtime times them out). For Tier 1
  and especially Tier 2 scenarios with Azure side effects, launch
  conservatively — a stop request can't recall an in-flight `azd provision`.
- **Keep waves small.** The wall-clock bottleneck on a fleet run is per-agent
  LLM time and per-account model concurrency, not the MCP server (which is
  per-`session_id`-parallel by design). Launching 4–6 sub-agents at a time and
  rolling forward typically finishes a sweep faster than launching everything
  at once.

## Driving conventions

These mirror the tester's own `AGENTS.md` ("Driving the MCP") — the driving agent
should follow them so the runs actually *test* the CLI instead of papering over
its bugs:

- **The scenario goals are the contract.** A scenario PASSES only when the
  product's actual behavior matches what the goals describe. If the goals say
  "expect error X" and the product prints a different error (even a reasonable
  one), that is a FAIL. If the goals reference a flag or subcommand that no
  longer exists, that is a FAIL. The driving agent's job is to **verify** goals
  were met, not to **rationalize** why they weren't. Do not mark a scenario as
  PASSED with an "observation" when the goals were not achieved — observations
  are for incidental notes on scenarios that genuinely passed all their goals.
- **Don't verify/retry after a `select`.** These runs exist to catch picker
  bugs; reading back the echo and "correcting" a pick hides the very defect the
  test is for. Send the action and let downstream prompts surface any failure.
- **Treat a select miss as a hard failure.** The tester's `select_by_text` is
  fail-loud: a missing target raises `LookupError`, surfaced as
  `ERROR during 'select': …`. **Report a finding and stop** — do not retry with a
  different `choice_text`/`choice_index` to work around it.
- **Never retry a failed scenario.** If a scenario fails (command errors,
  unexpected output, non-zero exit), report the finding and move on. Do **not**
  re-run the scenario hoping for a different result — unless the scenario's
  `goals:` explicitly instruct a retry. Retrying masks flaky behavior and makes
  the test suite unreliable as a regression signal.
- **Never adapt around broken goals.** If the goals instruct you to run a
  command or flag that does not exist, or expect output that does not appear,
  fail the scenario. Do not substitute an alternative command, skip the broken
  step, or invent a workaround. The scenario must be updated by a human — the
  driving agent must not silently patch over it.
- **Prefer `choice_text` over `choice_index`** when the label is stable (indices
  shift between releases).
- **Clear a pre-filled text field before typing.** Some prompts (e.g. the agent
  name) come pre-populated with an editable default; typing without clearing
  *appends* to it. Select-all then delete (or backspace) first so your value
  replaces the default instead of producing `defaultyourvalue`.
- **Pause before the first cloud-creating action.** Provisioning is expensive and
  irreversible-ish; confirm with the user before entering an `init`/`provision`
  flow that creates real resources (especially when running in parallel).
- **Pass `run_name=<scenario-stem>` to every `start_session` call.** The
  scenario stem is the YAML filename without `.yaml` (e.g. `00-version`,
  `21-show-json`, `27-run-local-and-invoke-local`). Without `run_name` the
  tester auto-names the run folder `agent_YYYYMMDD_HHMMSS`, which makes
  archived runs in `.reports/<run>/tester-reports/` hard to cross-reference
  with the scenario list. For scenarios that start two sessions
  (e.g. `27-run-local-and-invoke-local`), suffix the run_name with a role tag
  (`27-run-local-and-invoke-local-run`, `27-run-local-and-invoke-local-invoke`)
  so each session gets its own clearly named folder.
- **Pass `output_dir` to every `start_session` call** so the tester writes
  screenshots and HTML reports directly into this repo's archive layout
  instead of its own working directory. Use the WSL path of the
  `.reports/<run-timestamp>/tester-reports/` folder under this scenarios
  directory, with `<run-timestamp>` of the form `YYYYMMDD-HHMMSS`. Pick **one**
  `<run-timestamp>` per suite run and reuse it across every session — this
  groups all scenarios from one run under a single folder. The driving agent
  should also write the final cross-scenario summary to
  `.reports/<run-timestamp>/FINAL-REPORT.md` at the end. Example
  `output_dir` (the WSL view of this scenarios directory in this repo):
  `/mnt/c/Repos/azure-dev/cli/azd/extensions/azure.ai.agents/tests/cli-interactive-tester-scenarios/.reports/20260603-171132/tester-reports`.
  If your clone lives elsewhere, substitute the WSL path of *your*
  `cli/azd/extensions/azure.ai.agents/tests/cli-interactive-tester-scenarios/`.
- **Record a time-to-complete per scenario.** Capture wall-clock duration for
  every scenario (from `start_session` to `finish_session`, including pre/post
  hooks) and include it as a `Duration` column in the per-scenario tables of
  `FINAL-REPORT.md`. Use `Hh Mm Ss` formatting (e.g. `3m 21s`, `1h 04m 12s`).
  This makes regression slowdowns easy to spot across runs — Tier 2 in
  particular has scenarios that legitimately take many minutes (provision,
  deploy) and others that should complete in seconds.


## Tiers

Scenarios are organized into three tiers by cost and prerequisites. Each
scenario also carries a `tags:` list that exposes the same axes plus the
command(s) under test — see [Tags](#tags) for the full taxonomy and how to
filter via `list_scenarios`.

### Tier 0 — Offline (prefix `00-`)
No Azure auth, no network resource creation. Fast and deterministic. Safe to run
in any order, any time.

| File | Targets |
|------|---------|
| `00-version.yaml` | `version` |
| `00-help-root.yaml` | root help / command discovery |
| `00-sample-list-text.yaml` | `sample list` (text) |
| `00-sample-list-json-filters.yaml` | `sample list` `--output json`, `--language`, `--type`, `--featured-only` |
| `00-doctor-empty-dir.yaml` | `doctor` in an empty dir (graceful skips) |
| `00-doctor-local-only.yaml` | `doctor --local-only` |
| `00-init-validate-mutually-exclusive.yaml` | `init` arg validation (positional manifest + `-m`) |
| `00-init-validate-no-prompt-missing.yaml` | `init --no-prompt` missing-input error |
| `00-init-picker-navigation.yaml` | `init` interactive picker UX (abort before Azure) |
| `00-invoke-validate-protocol.yaml` | `invoke --protocol` unsupported-value error |
| `00-eval-context-required.yaml` | `eval list` outside a project requires a Foundry endpoint |
| `00-optimize-apply-requires-candidate.yaml` | `optimize apply` missing required `--candidate` |
| `00-doctor-partial-failure.yaml` | `doctor` mixed PASS+FAIL (exit 1) on a name-only `azure.yaml` |

### Tier 1 — Auth, scaffold only (prefix `10-`)
Requires Azure login (reads subscriptions/Foundry projects) but **does not
provision** any resources and incurs no cost. Each completes a project scaffold
and verifies the generated files, then stops before `azd provision`.

| File | Targets |
|------|---------|
| `10-init-template-python.yaml` | `init` new-from-template, Python |
| `10-init-template-dotnet.yaml` | `init` new-from-template, C#/.NET |
| `10-init-from-manifest-url.yaml` | `init -m <manifest url>` (needs `gh auth login`) |
| `10-init-from-code.yaml` | `init` → pick "Use the code in the current directory" |
| `10-init-flags-agent-name-model.yaml` | `init -m … --agent-name --model` (needs `gh auth login`) |
| `10-init-deploy-mode-code.yaml` | `init --deploy-mode code` (entry-point/runtime) |
| `10-init-validate-deploy-mode.yaml` | `init --deploy-mode` value validation (invalid value; code-mode required flags) — seeds from-code so the deploy-mode check is reached |
| `10-init-deploy-mode-container.yaml` | `init --deploy-mode container` (container build config) |

### Tier 2 — Cloud end-to-end (prefix `2x-`) — ⚠️ incurs Azure cost
Provisions real resources. **Run order matters:**

1. `20-setup-deploy-shared-agent.yaml` **first** — deploys the shared agent.
2. Any `21-`…`2A-` targeted scenario (reuse the deployed agent).
3. `2Z-teardown-down.yaml` **last** — `azd down --force --purge`.

All Tier 2 scenarios share one working tree under `~/working/azd-agents-shared`
so they operate on the same deployed agent. `20-setup` runs `init` there, which
scaffolds the project into the `{shared_agent_name}` subdirectory; the
reuse and teardown scenarios run with `~/working/azd-agents-shared/{shared_agent_name}`
as their `cwd`.

| File | Targets |
|------|---------|
| `20-setup-deploy-shared-agent.yaml` | `init` + `azd provision` + `azd deploy` (SETUP) |
| `21-show.yaml` | `show` (table) |
| `21-show-json.yaml` | `show --output json` |
| `22-invoke-remote.yaml` | `invoke` (remote) |
| `22-invoke-new-session.yaml` | `invoke --new-session` / `--new-conversation` (session vs conversation memory) |
| `22-invoke-input-file.yaml` | `invoke -f <file>` |
| `23-invoke-protocol-invocations.yaml` | `invoke --protocol invocations` (session-bound memory; `--new-session` resets, `--new-conversation` no-op) |
| `23-sessions-lifecycle.yaml` | `sessions create/list/show/delete` |
| `24-files-lifecycle.yaml` | `files upload/list/stat/mkdir/download/delete` |
| `25-monitor-console.yaml` | `monitor` (console) |
| `25-monitor-system.yaml` | `monitor --type system` |
| `26-endpoint-update.yaml` | `endpoint update` |
| `27-run-local-and-invoke-local.yaml` | `run` + `invoke --local` (two sessions) |
| `28-eval-lifecycle.yaml` | `eval init/run/list/show` against the shared agent (small sample budget, `--no-wait`) |
| `29-optimize-submit-and-cancel.yaml` | `optimize` submit + `list`/`status`/`cancel` (capped at 1 iteration, `--no-wait`) |
| `2A-doctor-provisioned-all-pass.yaml` | `doctor` (all checks pass) |
| `2Z-teardown-down.yaml` | `azd down --force --purge` (TEARDOWN) |

## Tags

Every scenario carries a top-level `tags:` list so an orchestrator can pick
subsets via the tester's `list_scenarios` MCP tool. The tool's filter is **OR
across the requested tags, case-sensitive, exact match**: a scenario matches
when its `tags` contains at least one of the requested values.

Three namespaces are used (all lowercase, kebab-case, colon-separated for
grouping — colons are treated as ordinary characters by the filter):

| Namespace | Values | Meaning |
|---|---|---|
| `tier:N` | `tier:0`, `tier:1`, `tier:2` | The tier the scenario belongs to (same axis as the directory's three sections above). Use this to express cost / auth profile in one tag. |
| `cmd:*` | `cmd:init`, `cmd:show`, `cmd:invoke`, `cmd:sessions`, `cmd:files`, `cmd:monitor`, `cmd:endpoint`, `cmd:run`, `cmd:doctor`, `cmd:eval`, `cmd:optimize`, `cmd:sample`, `cmd:down`, `cmd:provision`, `cmd:deploy`, `cmd:version`, `cmd:help` | The top-level `azd ai agent` (or `azd`) command(s) the scenario exercises. Multi-command scenarios (e.g. `27-run-local-and-invoke-local` runs both `run` and `invoke --local`; `20-setup` runs `init` + `provision` + `deploy`) carry multiple `cmd:*` tags. |
| traits | `parallel-safe`, `serial-only`, `negative-path`, `picker` | `parallel-safe` ↔ `serial-only` are mutually exclusive: all Tier 0 / Tier 1 scenarios are `parallel-safe`, all Tier 2 are `serial-only`. `negative-path` flags arg-/CLI-validation scenarios that assert errors or non-zero exit codes rather than happy-path success. `picker` flags scenarios whose primary purpose is exercising interactive picker UX. |

**Examples** (the tool's `tags:` parameter is OR across the list):

| Goal | `list_scenarios(tags=…)` |
|---|---|
| All `init` scenarios across every tier | `["cmd:init"]` |
| Everything offline (no Azure auth, no cost) | `["tier:0"]` |
| All Tier 2 cloud scenarios | `["tier:2"]` |
| Invoke + sessions reuse scenarios | `["cmd:invoke", "cmd:sessions"]` |
| CLI arg-validation scenarios only | `["negative-path"]` |
| Everything safe to run in parallel | `["parallel-safe"]` |

Sample prompt that uses tag filtering:

```
Use the cli-interactive-tester to run every `init` scenario across all tiers.

First, call list_scenarios with root="tests/cli-interactive-tester-scenarios"
and tags=["cmd:init"] to enumerate the matching scenarios.

Then read tests/cli-interactive-tester-scenarios/profile.yaml and
profile.local.yaml and merge them (local overrides shared); also derive
shared_agent_name = "{prefix}-{shared_agent_suffix}". Pass the merged map as
session_vars on every load_scenario / run_pre_hooks / start_session /
run_post_hooks call.

For each scenario returned by list_scenarios: load it, run any pre hooks,
start the session and accomplish the goals (take screenshots at each step),
finish the session, run any post hooks. The Tier 0/1 `init` scenarios are
parallel-safe (also tagged `parallel-safe`); fan them out via fleet mode.
The Tier 2 `init` scenario (`20-setup-deploy-shared-agent`) is `serial-only`
— run it on its own and only if I confirm I want to spend on Azure resources.
```

You can also get copilot to generate the tags list instead of manually specifying
it. For example, if you want to run all of the scenarios to test the changes
in a PR, modify the above prompt to start with something like:

```
Here's a PR: https://github.com/Azure/azure-dev/pull/8532. In the
tests\cli-interactive-tester-scenarios directory, there are a set of test scenarios,
with tags to categorize what they're testing. I want you to come up with a set of
tags which, when used to select these test scenarios, would properly test the
changes made by the PR provided.

Next, call list_scenarios with those tags, to enumerate matching scenarios.

Then read tests/cli-interactive-tester-scenarios/profile.yaml and ....
<remaining prompt from above>
```

And, if you're running these scenarios as a part of creating or reviewing a PR,
you can ask copilot to generate a summary report and add it as a comment directly
on the pull request.

When adding a new scenario, give it a `tags:` list that follows this
taxonomy: at minimum a `tier:N`, at least one `cmd:*`, and either
`parallel-safe` or `serial-only`. `list_scenarios` prints `tags: []` for any
file missing a `tags:` field, so an empty list in its output signals a
regression to fix.

> `list_scenarios` walks every `*.yaml` under the directory, including
> `profile.yaml` / `profile.local.yaml` (which surface as `(unnamed)` with
> `tags: (none)`). Filter by any `tier:*` / `cmd:*` / trait tag to exclude
> them — they intentionally carry no tags because they are configuration,
> not scenarios.

## Profile / overrides

Developer- and environment-specific values (subscription, region, model,
resource-name prefix, optional tenant) are **not** hardcoded in the scenario
YAMLs. Instead, the scenarios reference them via `{name}` placeholders, and
the orchestrator supplies the values as `session_vars` on every tester call.

Two files in this directory drive the values:

| File | Tracked? | Contents | Notes |
|---|---|---|---|
| `profile.yaml` | ✅ checked in | repo-shared defaults | `region`, `model`, `shared_agent_suffix` |
| `profile.local.yaml` | ❌ gitignored | per-developer / per-CI overrides | required: `prefix`, `subscription`. optional: `tenant` (no default) |
| `profile.local.yaml.example` | ✅ checked in | starter template | copy to `profile.local.yaml` and edit |

Variables exposed to scenarios via `session_vars`:

| Placeholder | Source | Default | Notes |
|---|---|---|---|
| `{prefix}` | `profile.local.yaml` | **required** | resource-name prefix; should be lowercase + hyphen-friendly so `sanitizeAgentName` doesn't mutate it |
| `{subscription}` | `profile.local.yaml` | **required** | subscription display name |
| `{tenant}` | `profile.local.yaml` | optional, no default | only consumed by the `az login` guidance above; when unset, drop `--tenant` and rely on the user's default tenant |
| `{region}` | `profile.yaml` | `East US 2` | |
| `{model}` | `profile.yaml` | `gpt-4.1-mini` | cheap/fast for tests |
| `{shared_agent_suffix}` | `profile.yaml` | `basic-responses` | |
| `{shared_agent_name}` | derived by orchestrator | `{prefix}-{shared_agent_suffix}` | Tier 2 subdirectory name — orchestrator must compute and pass alongside the others |

**Bootstrap (one-time per checkout):**

```sh
cp profile.local.yaml.example profile.local.yaml
# edit profile.local.yaml — set `prefix` (lowercase, hyphen-friendly) and `subscription`
```

The orchestrator must load both files, merge (local overrides shared), derive
`shared_agent_name`, and pass the merged map as `session_vars=` on every
`load_scenario` / `run_pre_hooks` / `start_session` / `run_post_hooks` call.
Failing to thread `session_vars` leaves `{prefix}` etc. unresolved in goals and
the run will execute against literal placeholder strings.

## Conventions

- **Tunable values** (subscription, region, model, prefix, tenant) come from
  the profile pair above — see [Profile / overrides](#profile--overrides).
- **Resource naming**: every newly created Azure resource (Foundry
  project/account, azd environment, agent, model deployment, resource group) is
  named with the `{prefix}-` value from your profile (and, in parallel-ready
  Tier 1 scenarios, a `-{instance}` suffix) so test resources are easy to
  identify, keep distinct across concurrent runs, and clean up. Note that some
  fields lowercase the value and replace invalid characters with hyphens — that
  normalization is expected (see `sanitizeAgentName` in the extension).
- `command:` invokes the installed extension as `azd ai agent …`.
- Init scenarios set `env: AZD_DISABLE_AGENT_DETECT: "1"` to disable agent
  auto-detection prompts.
- Every scenario asks the driver to screenshot key steps and file a finding
  (`report_finding`) for any confusing UX, error, or doc mismatch.

## Pre/post hooks

Scenarios use the tester's **`pre:`** and **`post:`** hook lists for host-side
setup and cleanup. Hooks run on the host (inside WSL on Windows), outside the
tmux session, **sequentially and fail-fast** unless a hook sets
`continue_on_error: true`. Each entry is a string or a mapping with `run`
(required), `cwd` (defaults to the scenario `cwd`, created if missing), `env`,
`continue_on_error` (default `false`), `timeout` (default **120s**), and `name`.

How they're used here:

- **`pre` reset** — stateful Tier 0/1 scenarios `rm -rf` their own working dir so
  re-runs start clean. (`start_session` recreates the dir, so removing it is
  enough; the doctor/init scenarios just need an empty dir.)
- **`pre` fixture seed** — the existing-code scenarios
  (`10-init-from-code`, `10-init-deploy-mode-code`) also copy a committed Python
  fixture into the dir so the source exists before the wizard's "Use the code in
  the current directory" flow inspects it (see [Fixtures](#fixtures)).
- **`pre` gh-auth guard** — the manifest scenarios (`10-init-from-manifest-url`,
  `10-init-flags-agent-name-model`) run `gh auth status` and fail fast if GitHub
  CLI isn't authenticated, because downloading the manifest can fall back to the
  `gh` CLI (and an interactive login) when the anonymous GitHub API is
  rate-limited. Run `gh auth login` first (see [Authentication](#authentication)).
- **`pre` idempotent setup (Tier 2)** — `20-setup-deploy-shared-agent` first runs
  `azd down --force --purge` if a leftover project exists in the shared dir (so it
  never orphans live Azure resources), then clears the dir. The down hook uses
  `timeout: 900` and `continue_on_error: true`.
- **`pre` precondition guard (Tier 2 reuse)** — `21-…2A` print a clear "run
  20-setup first" warning if the shared agent isn't deployed (non-fatal).
- **`post` cleanup** — `2Z-teardown-down` clears the shared working dir after the
  in-session `azd down` completes.

## Fixtures

`fixtures/from-code/` holds a minimal Python agent source tree (`app.py` +
`requirements.txt`) that satisfies the extension's existing-code detection
(it looks for `requirements.txt` or any `.py`, and defaults the entry point to
`app.py`). The existing-code scenarios copy it into the working dir via a `pre`
hook, then select "Use the code in the current directory" at the init prompt.

The hook references the fixture by absolute path with an overridable env var:

```sh
cp -r "${AZD_AGENTS_FIXTURES:-/mnt/c/Repos/azure-dev/cli/azd/extensions/azure.ai.agents/tests/cli-interactive-tester-scenarios/fixtures}/from-code/." "$cwd"
```

If your clone lives somewhere other than `/mnt/c/Repos/azure-dev` (the WSL view
of `C:\Repos\azure-dev`), export `AZD_AGENTS_FIXTURES` to the WSL path of this
`fixtures/` directory before running the existing-code scenarios.

## Re-running scenarios (idempotency)

Idempotency is handled **per scenario** via `pre`/`post` hooks rather than a
separate reset step — every scenario that holds state resets itself, so they can
be run back to back in any order within a tier:

- Tier 0/1 stateful scenarios **pre-wipe** their own `cwd`. Cleanup is pre-wipe
  **only** (no `post` delete), so the generated scaffold stays on disk for
  inspection after a run while the next run still starts clean.
- The shared Tier 2 dir is reset by `20-setup`'s `pre` hook, which **downs any
  leftover deployed project first** to avoid orphaning live Azure resources (this
  also sidesteps the resource-name hash collision behind
  [#8360](https://github.com/Azure/azure-dev/issues/8360)). `2Z-teardown-down`
  additionally clears the dir in a `post` hook.
- Read-only scenarios (`version`, `--help`, `sample list`) run in `/tmp`, hold no
  state, and declare no hooks.

> If a Tier 2 run is interrupted before `2Z-teardown`, just re-run
> `20-setup-deploy-shared-agent` — its `pre` hook downs any live project in the
> shared dir before redeploying, so resources won't be orphaned.

## Notes

- `files` and `sessions` are exercised as one lifecycle scenario per command
  group (rather than one file per subcommand) to avoid cross-scenario ordering
  dependencies — still one command at a time.
- `azd ai agent run` blocks the terminal; `27-run-local-and-invoke-local.yaml`
  uses two sessions (one to run, one to invoke `--local`) that share an
  allocated `{agent}` port (see
  [Parallel-readiness](#parallel-readiness--port-allocation)).
- Run artifacts (screenshots, HTML reports) land in `.reports/`, which is
  git-ignored.
