<!-- cspell:ignore benhanrahan azdaiagent -->
# `azd ai agent` — cli-interactive-tester scenarios

Goal-based scenarios for driving the `azure.ai.agents` extension through the
[cli-interactive-tester](https://github.com/coreai-microsoft/cli-interactive-tester)
MCP server. Each file targets **one** command or flow at a time and uses the
strict `goals:` list format so the run is repeatable and reviewable.

## How to run

Register the cli-interactive-tester MCP server (see its README), then ask
Copilot CLI to load a scenario and accomplish its goals, e.g.:

```
Use the cli-interactive-tester to load the scenario at
tests/cli-interactive-tester-scenarios/00-version.yaml. If it declares pre hooks,
run them first; then start the session, accomplish the goals, take screenshots at
each step, and run any post hooks after finishing.
```

Most scenarios here declare **`pre:` hooks** (host-side setup such as resetting
the working dir or seeding a fixture), and a few declare **`post:` hooks**
(cleanup). The agent must invoke them via the tester's `run_pre_hooks` /
`run_post_hooks` MCP tools — `load_scenario` surfaces whether a scenario has any.
See [Pre/post hooks](#prepost-hooks) below.

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
  deployed project actually lives in `~/working/azd-agents-shared/trangevi-basic-responses`;
  the reuse and teardown scenarios run with that subdirectory as their `cwd`.

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

## Authentication

Tier 1 and Tier 2 scenarios read from / write to Azure, so a **human must log in
manually before** starting a run. The scenarios do **not** perform login
themselves, and the test-driving agent **cannot** complete it either: `az login`
opens a **separate browser window** for account selection that requires
human interaction outside the terminal the agent controls. Treat auth as a
one-time manual prerequisite, not a scenario step.

Inside WSL, a human runs:

```
az login --tenant azdaiagent.onmicrosoft.com
```

This opens the interactive sign-in flow and then:

1. **Browser account selection** — a separate browser window opens; the human
   picks the account in the `azdaiagent.onmicrosoft.com` tenant. (The agent
   cannot do this.)
2. **Subscription selection** — back in the terminal, select the
   `azd ai agent development` subscription.

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
  `trangevi-basic-responses` subdirectory created by `20-setup`). They are
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

- **Don't verify/retry after a `select`.** These runs exist to catch picker
  bugs; reading back the echo and "correcting" a pick hides the very defect the
  test is for. Send the action and let downstream prompts surface any failure.
- **Treat a select miss as a hard failure.** The tester's `select_by_text` is
  fail-loud: a missing target raises `LookupError`, surfaced as
  `ERROR during 'select': …`. **Report a finding and stop** — do not retry with a
  different `choice_text`/`choice_index` to work around it.
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

Scenarios are organized into three tiers by cost and prerequisites.

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

### Tier 2 — Cloud end-to-end (prefix `2x-`) — ⚠️ incurs Azure cost
Provisions real resources. **Run order matters:**

1. `20-setup-deploy-shared-agent.yaml` **first** — deploys the shared agent.
2. Any `21-`…`2A-` targeted scenario (reuse the deployed agent).
3. `2Z-teardown-down.yaml` **last** — `azd down --force --purge`.

All Tier 2 scenarios share one working tree under `~/working/azd-agents-shared`
so they operate on the same deployed agent. `20-setup` runs `init` there, which
scaffolds the project into the `trangevi-basic-responses` subdirectory; the
reuse and teardown scenarios run with `~/working/azd-agents-shared/trangevi-basic-responses`
as their `cwd`.

| File | Targets |
|------|---------|
| `20-setup-deploy-shared-agent.yaml` | `init` + `azd provision` + `azd deploy` (SETUP) |
| `21-show.yaml` | `show` (table) |
| `21-show-json.yaml` | `show --output json` |
| `22-invoke-remote.yaml` | `invoke` (remote) |
| `22-invoke-new-session.yaml` | `invoke --new-session` / `--new-conversation` (session vs conversation memory) |
| `22-invoke-input-file.yaml` | `invoke -f <file>` |
| `23-sessions-lifecycle.yaml` | `sessions create/list/show/delete` |
| `24-files-lifecycle.yaml` | `files upload/list/stat/mkdir/download/delete` |
| `25-monitor-console.yaml` | `monitor` (console) |
| `25-monitor-system.yaml` | `monitor --type system` |
| `26-endpoint-update.yaml` | `endpoint update` |
| `27-run-local-and-invoke-local.yaml` | `run` + `invoke --local` (two sessions) |
| `2A-doctor-provisioned-all-pass.yaml` | `doctor` (all checks pass) |
| `2Z-teardown-down.yaml` | `azd down --force --purge` (TEARDOWN) |

## Conventions

- **Subscription**: `azd ai agent development`
- **Region**: `East US 2`
- **Model**: `gpt-4.1-mini` (cheap/fast for testing)
- **Resource name prefix**: every newly created Azure resource (Foundry
  project/account, azd environment, agent, model deployment, resource group) is
  named with a `trangevi-` prefix (and, in parallel-ready Tier 1 scenarios, a
  `-{instance}` suffix) so test resources are easy to identify, keep distinct
  across concurrent runs, and clean up. Note that some fields lowercase the value
  and replace invalid characters with hyphens — that normalization is expected
  (see `sanitizeAgentName` in the extension).
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
