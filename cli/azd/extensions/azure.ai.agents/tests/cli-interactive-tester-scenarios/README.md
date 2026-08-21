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

**You don't drive these scenarios by hand.** Runs are executed by agents so they stay
deterministic and fail-loud. Pick the **`foundry-extension-scenario-orchestrator`** agent as your front door
and tell it what you want; it routes to a run skill and fans the work out to
`foundry-extension-scenario-worker` agents:

- **Testing a PR / your change** → it uses the **`foundry-extension-scenario-pr-regression`** skill (maps the
  diff to impacted scenarios and posts a results comment).
- **A full or tag/tier sweep** ("run everything", "all `init` scenarios", "all of Tier 2") →
  it uses the **`foundry-extension-scenario-suite-run`** skill.

The orchestrator (or the run skill) **loads both profile files, merges them (local overrides
shared), generates one run ID with seconds plus a short random suffix, derives
`shared_agent_name = {prefix}-{shared_agent_suffix}-{run_id}`, and passes a per-scenario map as
`session_vars` on every `load_scenario`, `run_pre_hooks`, `start_session`, and
`run_post_hooks` call**. For parallel-safe scenarios that map also includes the assigned
`instance`, matching the `instance_id` passed to hooks and sessions. The scenario YAMLs
reference those values via `{prefix}`, `{subscription}`, `{region}`, `{model}`, `{tenant}`
(optional), `{shared_agent_name}`, and `{instance}` placeholders. The step-by-step driving
rules those agents follow live in
[`driving-mechanics.md`](./driving-mechanics.md).

Most scenarios here declare **`pre:` hooks** (host-side setup such as resetting
the working dir or seeding a fixture), and a few declare **`post:` hooks**
(cleanup). The agent must invoke them via the tester's `run_pre_hooks` /
`run_post_hooks` MCP tools — `load_scenario` surfaces whether a scenario has any.
See [Pre/post hooks](#prepost-hooks) below.

To run **everything**, select the `foundry-extension-scenario-orchestrator` agent and state your intent and
cost consent, e.g. *"Run the full scenario suite across all tiers; I accept the Tier 1b /
Tier 2 Azure cost."* It discovers the scenarios, enforces the prerequisite and `azd`-binary
gates, validates the recipe on one scenario, fans Tier 0/1 out in parallel waves, runs Tier 1b
after its Tier 1 prerequisites pass, runs Tier 2 serially, and writes a final report.

For a **subset** (e.g. "just the `init` scenarios" or "everything in Tier 0") name the subset
instead — the `foundry-extension-scenario-suite-run` skill filters by `tags:` via
`list_scenarios`. The concrete plan automatically adds every selected scenario's `requires:`
prerequisites; for example, a `verify-deploy` filter also adds the Tier 1 scenarios that create
the scaffolds. If any Tier 2 scenario matches, the plan also adds `2.00-setup` and
`2.99-teardown` so filtered runs retain their full resource lifecycle. See [Tags](#tags) for
the taxonomy.

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
  isolation (the run-scoped `{instance}` suffix keeps distinct scenarios and concurrent
  sweeps apart — see [Parallel-readiness](#parallel-readiness--port-allocation)); and a
  single shared `~/working/azd-agents-shared` dir for all Tier 2 scenarios so they
  operate on the same deployed agent. `2.00-setup` runs `init` in that shared dir,
  which scaffolds the project into a subdirectory named after the agent, so the
  deployed project actually lives in `~/working/azd-agents-shared/{shared_agent_name}`
  (where `{shared_agent_name} = {prefix}-{shared_agent_suffix}-{run_id}` from your
  [profile](#profile--overrides), e.g. `alice-basic-responses-0714103842-a1b2c3`); the reuse and
  teardown scenarios run with that subdirectory as their `cwd`.

On macOS/Linux these are simply native paths (no WSL involved).

### This applies to MCP tool arguments too

Driving agents must pass **WSL-style paths** to every path-shaped argument on the tester's MCP
tools (`path:` on `load_scenario` / `run_pre_hooks` / `run_post_hooks`, and `scenario_path:` /
`output_dir:` on `start_session`). The full rule, the Windows/macOS/Linux table, and the
`Scenario file not found` failure-mode hint live in the executor spec:
[`driving-mechanics.md`](./driving-mechanics.md) § Path style (Windows → WSL).

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
1. Reads the required Go version from `cli/azd/go.mod` and, when necessary, downloads the
   matching official WSL architecture build from `go.dev`, verifies its SHA-256 checksum, and
   installs it under `/usr/local/go`
2. Reads the required .NET SDK version from `dotnet-sdk.version` and, when necessary, downloads
   the matching official WSL architecture build from Microsoft's release metadata, verifies its
   SHA-512 checksum, and installs it under `/usr/local/dotnet`
3. Builds `azd` core for the native WSL architecture → `/usr/local/bin/azd`
4. Ensures the extensions dev kit (`microsoft.azd.extensions`) supports
   `azd x pack --bundle`, installing or upgrading it when needed
5. Builds, packages, and installs the `azure.ai.agents` extension from source
   using `azd x build` → `azd x pack --bundle` → `azd extension install`
6. Verifies azd, the extension, and the pinned .NET SDK report expected versions

The script properly registers the extension in azd's config, so it will always
use your dev build — never the published registry version.

**Re-run `setup-wsl.sh` after every local code change** you want to test.
It supports x86-64 and ARM64 WSL environments. Go and .NET do not need to be installed first;
the script bootstraps the exact pinned versions. It requires network access to `go.dev` and
Microsoft's .NET release/download endpoints, Git, `curl` or `wget`, `awk`, `grep`, `tar`,
`sha256sum`, `sha512sum`, `uname`, and sudo access in WSL. It does not install general OS
packages or modify shell startup files.

On native Linux or macOS, do not run `setup-wsl.sh`. Build and install the repository's
development `azd` and extension through your normal local workflow.

## Authentication

Tier 1, Tier 1b, and Tier 2 scenarios read from / write to Azure, so a **human must log in
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

During a scenario, the product may also show a terminal `Select a tenant`
prompt when the signed-in user can access multiple tenants. The worker selects
the tenant configured in `profile.local.yaml`. If no tenant is configured, the
scenario fails without answering rather than guessing; users with only one
tenant remain supported because the prompt does not appear.

Tier 0 (`tier0/`) scenarios need no auth. Run this `az login` step once per WSL
session **before** asking the agent to drive any Tier 1/Tier 2 scenario; all of
them reuse that session credential.

### GitHub login (manifest scenarios)

The manifest scenarios (`1.03-init-from-azure-yaml-url`,
`1.05-init-flag-agent-name`) download an agent manifest — and its sibling
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

- **`{instance}` substitution.** The orchestrator derives a safe value from the scenario key
  and sweep run ID (for example, `t101-0714103842-a1b2c3`). It supplies that value as both
  `session_vars.instance` and `instance_id` to expose `{instance}` consistently in `command`,
  `cwd`, `env`, hook fields, and `goals`. Although the tester defaults to `"main"` when omitted,
  parallel-safe fleet scenarios must not rely on that default.
- **Which scenarios are parallel-ready:**
  - **Tier 0 work-dir scenarios** (`doctor`, picker, validate) and **all Tier 1
    `init` scenarios** suffix their `cwd` (and hook paths) with `-{instance}`, so
    distinct scenarios and concurrent sweeps get isolated working directories.
  - **Tier 1 resource names** are suffixed with `-{instance}` too (via the
    RESOURCE NAMING goal and the `--agent-name` flag), so parallel workers do not collide on
    Azure resource names. Each Tier 1b verifier reuses its prerequisite's exact instance.
  - **`2.12-run-local-and-invoke-local`** declares `allocate_ports: [agent]` and
    binds `azd ai agent run`/`invoke --local` to `--port {agent}`. A port pool is
    shared across every `start_session` with the same `scenario_path`, so the
    run and invoke sessions find each other without using `instance_id`. Their
    session IDs use the sweep-wide `{run_id}` instead.
- **Single-instance by design:** the **Tier 2 reuse scenarios** (`2.01-`…`2.18-`),
  plus `2.00-setup` and `2.99-teardown`, all share the one deployed agent under
  `~/working/azd-agents-shared` (the project itself lives in the
  `{shared_agent_name}` subdirectory created by `2.00-setup`). They are
  **not** parameterized with `{instance}` (doing so would break the shared-agent
  assumption) and should be run serially.

How the executor actually fans these out (per-instance `session_id`s, wave sizes, tier
ordering) is specified in [`driving-mechanics.md`](./driving-mechanics.md) § Parallelism &
ordering — this section only documents which scenarios are *authored* to support concurrency.

## Orchestrating a fleet run

Fleet orchestration — fan-out primitives, per-`session_id` timestamps, wave sizes, Tier 1b /
Tier 2 ordering, and the operational guardrails (validate the recipe first, launch
cost-incurring waves conservatively, keep waves small) — is the executor's job and is
specified once in [`driving-mechanics.md`](./driving-mechanics.md) § Parallelism & ordering. In
practice you don't orchestrate by hand: the `foundry-extension-scenario-orchestrator` agent runs that flow and
spawns one `foundry-extension-scenario-worker` per scenario.

## How scenarios are judged (authoring contract)

These are the rules that decide whether a scenario **passes**, so they matter most when you
*author* goals — write goals that hold under them. This is the human-facing half of the
contract; the operational rules the executor follows (select handling, retries,
`run_name` / `output_dir` / duration capture, environment integrity, path style) live in
[`driving-mechanics.md`](./driving-mechanics.md). A driving agent applies both.

- **The scenario goals are the contract.** A scenario PASSES only when the product's actual
  behavior matches what the goals describe. If the goals say "expect error X" and the product
  prints a different error (even a reasonable one), that is a FAIL. If the goals reference a
  flag or subcommand that no longer exists, that is a FAIL. So write goals as the *literal,
  verifiable* spec of correct behavior — the driver's job is to **verify** them, not to
  **rationalize** why they weren't met, and it will not mark a scenario PASSED with an
  "observation" when the goals were not achieved.
- **Every product prompt must be expected.** The goals define the complete set of interactive
  product prompts allowed during the flow. A prompt is expected only when a goal describes it,
  including explicitly optional prompts written as "if asked." Any other product prompt is a
  behavior mismatch: the driver reports it, fails the scenario, and does not answer it
  speculatively. Shell prompts and the interactive tester's own UI are not product prompts.
  Avoid open-ended directions such as "follow the prompts," which make the expected UX
  unverifiable.
- **Capture directives are the narrow exception.** Screenshots are best-effort supporting
  evidence, not product behavior. A screenshot error or timeout must produce an observation
  identifying the capture error and unavailable evidence, but it does not block later goals or
  fail an otherwise passing scenario. The executor does not retry screenshot capture. A broken
  terminal/session that prevents product verification remains an infrastructure failure.
- **Never adapt around broken goals.** If a goal instructs a command or flag that does not
  exist, or expects output that does not appear, the driver **fails** the scenario rather than
  substituting an alternative, skipping the step, or inventing a workaround. Keep goals current
  so this doesn't happen — a broken goal must be fixed by a human, not patched over at run time.
- **Give every executed invoke explicit input.** Write the literal command with a quoted
  positional message, such as `azd ai agent invoke "Hello, are you there?"`. Use
  `--input-file` only when file input is the behavior under test, and define the file contents
  before invoking. Do not tell the driver to run bare `invoke` and then "send a message": the
  CLI requires the message or input-file argument in the command and does not prompt for it.
- **Prefer stable labels.** When a goal drives an interactive picker, key it off a stable text
  label rather than a positional index — the driver prefers `choice_text` over `choice_index`
  because indices shift between releases.
- **Describe multi-select outcomes, not driving mechanics.** Name the user-visible prompt and
  use one of two explicit goal forms:
  - A **default assertion** states which choices must already be selected and unselected, then
    instructs the user to leave that selection unchanged and continue. A mismatch fails without
    repair because the default itself is product behavior under test.
  - A **final-state instruction** states which choices should end selected and unselected without
    asserting their initial state. The driver may change only the differences needed to reach
    that outcome.
  An already-correct selection always advances without changing any choice. Keep action names,
  indices, payloads, and keystrokes out of scenario goals; the driver owns those mechanics.
- **Pause before the first cloud-creating action.** Provisioning is expensive and
  irreversible-ish; a run must have explicit cost consent before entering any `init` /
  `provision` flow that creates real resources (especially in parallel).


## Tiers

Scenarios are organized into four tiers by cost and prerequisites. Each
scenario also carries a `tags:` list that exposes the same axes plus the
command(s) under test — see [Tags](#tags) for the full taxonomy and how to
filter via `list_scenarios`.

### Tier 0 — Offline (`tier0/`)
No Azure auth, no network resource creation (except `sample list`, which fetches
the public template catalog). Fast and mostly deterministic. Safe to run
in any order, any time.

| File | Targets |
|------|---------|
| `tier0/0.01-version.yaml` | `version` |
| `tier0/0.02-help-root.yaml` | root help / command discovery |
| `tier0/0.03-sample-list-text.yaml` | `sample list` (text) |
| `tier0/0.04-sample-list-json-filters.yaml` | `sample list` `--output json`, `--language`, `--type`, `--featured-only` |
| `tier0/0.05-doctor-empty-dir.yaml` | `doctor` in an empty dir (graceful skips) |
| `tier0/0.06-doctor-local-only.yaml` | `doctor --local-only` |
| `tier0/0.07-doctor-partial-failure.yaml` | `doctor` mixed PASS+FAIL (exit 1) on a name-only `azure.yaml` |
| `tier0/0.08-init-validate-mutually-exclusive.yaml` | `init` arg validation (positional manifest + `-m`) |
| `tier0/0.09-init-validate-no-prompt-missing.yaml` | `init --no-prompt` missing-input error |
| `tier0/0.10-init-picker-navigation.yaml` | `init` interactive picker UX (abort before Azure) |
| `tier0/0.11-invoke-validate-protocol.yaml` | `invoke --protocol` unsupported-value error |
| `tier0/0.12-eval-context-required.yaml` | `eval list` outside a project requires a Foundry endpoint |
| `tier0/0.13-optimize-apply-requires-candidate.yaml` | `optimize apply` missing required `--candidate` |
| `tier0/0.14-endpoint-show-help.yaml` | `endpoint show --help` |
| `tier0/0.15-code-download-help.yaml` | `code download --help` |
| `tier0/0.16-delete-help.yaml` | `delete --help` |

### Tier 1 — Auth, scaffold only (`tier1/`)
Requires Azure login (reads subscriptions/Foundry projects) but **does not
provision** any resources and incurs no cost. Each completes a project scaffold
and verifies the generated files, then stops before `azd provision`.

| File | Targets |
|------|---------|
| `tier1/1.01-init-template-python.yaml` | `init` new-from-template, Python |
| `tier1/1.02-init-template-dotnet.yaml` | `init` new-from-template, C#/.NET |
| `tier1/1.03-init-from-azure-yaml-url.yaml` | `init -m <manifest url>` (needs `gh auth login`) |
| `tier1/1.04-init-from-code.yaml` | `init` → pick "Use the code in the current directory" |
| `tier1/1.05-init-flag-agent-name.yaml` | `init -m … --agent-name` (needs `gh auth login`) |
| `tier1/1.06-init-deploy-mode-code.yaml` | `init --deploy-mode code` (entry-point/runtime) |
| `tier1/1.07-init-deploy-mode-container.yaml` | `init --deploy-mode container` (container build config) |
| `tier1/1.08-init-validate-deploy-mode.yaml` | `init --deploy-mode` value validation (invalid value; code-mode required flags) — seeds from-code so the deploy-mode check is reached |

### Tier 1b — Deploy-verify (`tier1b/`) — ⚠️ incurs Azure cost
Verifies that Tier 1 scaffolds actually **deploy** and produce a working agent.
Each scenario reuses the on-disk scaffold from a Tier 1 init run (no init
duplication), provisions its own Azure resources, deploys, checks the agent is
accessible, then tears down with `azd down`. Independent Azure environments per
scenario — safe to parallelize once prerequisites pass.

Each scenario declares a `requires:` field pointing to the Tier 1 scenario
whose scaffold it deploys. The orchestrator **must** check this: if the
prerequisite didn't PASS in the current run, the Tier 1b scenario is SKIPPED.

The scaffold path depends on the init flow and is explicit in each Tier 1b
scenario:

- Template flows (`1.01`, `1.02`) create a nested directory named for the requested agent.
- Manifest URL flows (`1.03`, `1.05`) retain the downloaded project's directory,
  `agent-framework-agent-basic-responses`; `--agent-name` changes the Foundry agent identity,
  not that local directory.
- Current-directory flows (`1.04`, `1.06`, `1.07`) write the scaffold directly into their
  seeded working directory.

Tier 1b intentionally does not search for `azure.yaml`: an upstream layout change fails the
producer/consumer contract clearly instead of deploying an arbitrary directory.

| File | Verifies | Requires |
|------|----------|----------|
| `tier1b/1b.01-deploy-template-python.yaml` | Python scaffold deploys | `tier1/1.01-init-template-python.yaml` |
| `tier1b/1b.02-deploy-template-dotnet.yaml` | .NET scaffold deploys | `tier1/1.02-init-template-dotnet.yaml` |
| `tier1b/1b.03-deploy-from-azure-yaml-url.yaml` | URL-based scaffold deploys | `tier1/1.03-init-from-azure-yaml-url.yaml` |
| `tier1b/1b.04-deploy-from-code.yaml` | From-code scaffold deploys | `tier1/1.04-init-from-code.yaml` |
| `tier1b/1b.05-deploy-flag-agent-name.yaml` | Agent-name flag scaffold deploys | `tier1/1.05-init-flag-agent-name.yaml` |
| `tier1b/1b.06-deploy-deploy-mode-code.yaml` | Code-deploy scaffold deploys | `tier1/1.06-init-deploy-mode-code.yaml` |
| `tier1b/1b.07-deploy-deploy-mode-container.yaml` | Container-deploy scaffold deploys | `tier1/1.07-init-deploy-mode-container.yaml` |

### Tier 2 — Cloud end-to-end (`tier2/`) — ⚠️ incurs Azure cost
Provisions real resources. **Run order matters:**

1. `tier2/2.00-setup-deploy-shared-agent.yaml` **first** — deploys the shared agent.
2. Any `2.01-`…`2.18-` targeted scenario (reuse the deployed agent).
3. `tier2/2.99-teardown-down.yaml` **last** — `azd down --force --purge`.

All Tier 2 scenarios share one working tree under `~/working/azd-agents-shared`
so they operate on the same deployed agent. `2.00-setup` runs `init` there, which
scaffolds the project into the `{shared_agent_name}` subdirectory; the
reuse and teardown scenarios run with `~/working/azd-agents-shared/{shared_agent_name}`
as their `cwd`.

| File | Targets |
|------|---------|
| `tier2/2.00-setup-deploy-shared-agent.yaml` | `init` + `azd provision` + `azd deploy` (SETUP) |
| `tier2/2.01-show.yaml` | `show` (table) |
| `tier2/2.02-show-json.yaml` | `show --output json` |
| `tier2/2.03-invoke-remote.yaml` | `invoke` (remote) |
| `tier2/2.04-invoke-new-session.yaml` | `invoke --new-session` / `--new-conversation` (session vs conversation memory) |
| `tier2/2.05-invoke-input-file.yaml` | `invoke -f <file>` |
| `tier2/2.06-invoke-protocol-invocations.yaml` | `invoke --protocol invocations` (session-bound memory; `--new-session` resets, `--new-conversation` no-op) |
| `tier2/2.07-sessions-lifecycle.yaml` | `sessions create/list/show/delete` |
| `tier2/2.08-files-lifecycle.yaml` | `files upload/list/stat/mkdir/download/delete` |
| `tier2/2.09-monitor-console.yaml` | `monitor` (console) |
| `tier2/2.10-monitor-system.yaml` | `monitor --type system` |
| `tier2/2.11-endpoint-update.yaml` | `endpoint update` |
| `tier2/2.12-run-local-and-invoke-local.yaml` | `run` + `invoke --local` (two sessions) |
| `tier2/2.15-doctor-provisioned-all-pass.yaml` | `doctor` (all checks pass) |
| `tier2/2.16-endpoint-show.yaml` | `endpoint show` (agent endpoint details) |
| `tier2/2.17-code-download.yaml` | `code download` (positive-path: downloads agent source code) |
| `tier2/2.18-delete.yaml` | `delete` (destroys the shared agent — run before teardown) |
| `tier2/2.99-teardown-down.yaml` | `azd down --force --purge` (TEARDOWN) |

## Tags

Every scenario carries a top-level `tags:` list so an orchestrator can pick
subsets via the tester's `list_scenarios` MCP tool. The tool's filter is **OR
across the requested tags, case-sensitive, exact match**: a scenario matches
when its `tags` contains at least one of the requested values.

Three namespaces are used (all lowercase, kebab-case, colon-separated for
grouping — colons are treated as ordinary characters by the filter):

| Namespace | Values | Meaning |
|---|---|---|
| `tier:N` | `tier:0`, `tier:1`, `tier:1b`, `tier:2` | The tier the scenario belongs to (same axis as the directory's four sections above). Use this to express cost / auth profile in one tag. |
| `cmd:*` | `cmd:init`, `cmd:show`, `cmd:invoke`, `cmd:sessions`, `cmd:files`, `cmd:monitor`, `cmd:endpoint`, `cmd:run`, `cmd:doctor`, `cmd:eval`, `cmd:optimize`, `cmd:sample`, `cmd:down`, `cmd:provision`, `cmd:deploy`, `cmd:version`, `cmd:help`, `cmd:code`, `cmd:delete` | The top-level `azd ai agent` (or `azd`) command(s) the scenario exercises. Multi-command scenarios (e.g. `2.12-run-local-and-invoke-local` runs both `run` and `invoke --local`; `2.00-setup` runs `init` + `provision` + `deploy`) carry multiple `cmd:*` tags. |
| traits | `parallel-safe`, `serial-only`, `negative-path`, `picker`, `verify-deploy` | `parallel-safe` ↔ `serial-only` are mutually exclusive: all Tier 0 / Tier 1 / Tier 1b scenarios are `parallel-safe`, all Tier 2 are `serial-only`. `negative-path` flags arg-/CLI-validation scenarios that assert errors or non-zero exit codes rather than happy-path success. `picker` flags scenarios whose primary purpose is exercising interactive picker UX. `verify-deploy` flags Tier 1b scenarios that verify a Tier 1 scaffold deploys. |

**Examples** (the tool's `tags:` parameter is OR across the list):

| Goal | `list_scenarios(tags=…)` |
|---|---|
| All `init` scenarios across every tier | `["cmd:init"]` |
| Everything offline (no Azure auth, no cost) | `["tier:0"]` |
| All Tier 1b verify-deploy scenarios | `["verify-deploy"]` |
| All Tier 2 cloud scenarios | `["tier:2"]` |
| Invoke + sessions reuse scenarios | `["cmd:invoke", "cmd:sessions"]` |
| CLI arg-validation scenarios only | `["negative-path"]` |
| Everything safe to run in parallel | `["parallel-safe"]` |

To run a **tag or tier subset**, use the `foundry-extension-scenario-suite-run` skill (or the
`foundry-extension-scenario-orchestrator` agent) and name the subset — e.g. *"run every `init` scenario across
all tiers"* or *"run everything tagged `parallel-safe`"*. It calls `list_scenarios` with the
right tags, applies the cost gate for any Tier 1b / Tier 2 members, and drives them.

To **test a PR**, use the `foundry-extension-scenario-pr-regression` skill (or the `foundry-extension-scenario-orchestrator`
agent): it maps the PR's changed files to the impacted tags automatically, enumerates the
matching scenarios, runs them, and posts a summary report as a PR comment.

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

## The `requires:` field

A scenario may declare a `requires:` field with a path (relative to the scenarios
root) to another scenario that must PASS before this one can run:

```yaml
requires: "tier1/1.01-init-template-python.yaml"
```

**Semantics:**

- The orchestrator checks the prerequisite's result in the current run.
- If the prerequisite **PASSED** → proceed with this scenario normally.
- If the prerequisite **FAILED / was not run / was SKIPPED** → mark this
  scenario as ⏭️ SKIPPED with reason "prerequisite `<path>` did not pass".
- The `requires:` value is always a **relative path** from the scenarios root
  (e.g. `tier1/1.01-init-template-python.yaml`, not an absolute path).
- The cli-interactive-tester MCP server ignores unknown top-level YAML keys,
  so `requires:` is purely orchestrator-side logic — it doesn't affect
  `load_scenario` or session behavior.

**When to use:** Tier 1b verify-deploy scenarios use `requires:` to express
their dependency on the Tier 1 init scaffold they deploy. This ensures the
orchestrator doesn't waste time (and Azure cost) attempting to deploy a
scaffold that failed to initialize.

## Profile / overrides

Developer- and environment-specific values (subscription, region, model,
resource-name prefix, optional tenant) are **not** hardcoded in the scenario
YAMLs. Most are referenced via `{name}` placeholders. The optional tenant is
passed in `session_vars`, but goals refer to the tenant provided with the
scenario run without a literal placeholder so scenarios still load when tenant
is omitted. The orchestrator supplies the merged values on every tester call.

Two files in this directory drive the values:

| File | Tracked? | Contents | Notes |
|---|---|---|---|
| `profile.yaml` | ✅ checked in | repo-shared defaults | `region`, `model`, `shared_agent_suffix` |
| `profile.local.yaml` | ❌ gitignored | per-developer / per-CI overrides | required: `prefix`, `subscription`. optional: `tenant` (no default) |
| `profile.local.yaml.example` | ✅ checked in | starter template | copy to `profile.local.yaml` and edit |

Variables exposed to scenarios via `session_vars`:

| Variable | Source | Default | Notes |
|---|---|---|---|
| `{prefix}` | `profile.local.yaml` | **required** | resource-name prefix; should be lowercase + hyphen-friendly so `sanitizeAgentName` doesn't mutate it |
| `{subscription}` | `profile.local.yaml` | **required** | subscription display name |
| `{tenant}` | `profile.local.yaml` | optional, no default | scopes `az login` when provided and supplies product tenant pickers; when unset, omit `--tenant`, but fail without answering if a picker appears |
| `{region}` | `profile.yaml` | `East US 2` | |
| `{model}` | `profile.yaml` | `gpt-5.4-mini` | cheap/fast for tests |
| `{shared_agent_suffix}` | `profile.yaml` | `basic-responses` | |
| `{run_id}` | derived by orchestrator | 10-digit month/day/hour/minute/second timestamp plus 6 lowercase hexadecimal characters | Generated once per sweep and reused for artifacts, sessions, and resource identity. |
| `{shared_agent_name}` | derived by orchestrator | `{prefix}-{shared_agent_suffix}-{run_id}` | Tier 2 subdirectory and agent name. Seconds plus the random suffix isolate concurrent runs. |
| `{instance}` | derived per scenario | `<scenario-key>-{run_id}` | Tier 0/Tier 1 parallel-safe identity; Tier 1b reuses its prerequisite's exact value. |
| `{fixtures_dir}` | derived by orchestrator | `<scenarios-dir>/fixtures` | Tester-side absolute path to the `fixtures/` subdirectory (WSL-translated on Windows, native on Linux/macOS); used by pre-hooks to seed test fixture files |

**Bootstrap (one-time per checkout):**

```sh
cp profile.local.yaml.example profile.local.yaml
# edit profile.local.yaml — set `prefix` (lowercase, hyphen-friendly) and `subscription`
```

The orchestrator must load both files, merge local overrides over shared defaults, generate
one `run_id`, and derive `shared_agent_name` and `fixtures_dir` (the tester-side absolute path
of the `fixtures/` subdirectory — WSL-translated on Windows, native on Linux/macOS). For each
parallel-safe scenario it adds the assigned `instance` to a per-scenario copy of that map.
It passes the map as `session_vars=` on every `load_scenario` / `run_pre_hooks` /
`start_session` / `run_post_hooks` call and passes the matching `instance_id` to every hook or
session tool that accepts it. Failing to thread either value can render and execute different
paths or leave literal placeholders unresolved.

Every `{name}`-shaped token in a scenario command, path, hook, or goal is
interpreted as a placeholder. Embedded shell syntax must not accidentally use
that shape: for example, write an awk action as `{ print }`, not `{print}`.
Static validation should reject every brace-delimited token that is not a known
profile/session variable.

## Conventions

- **Tunable values** (subscription, region, model, prefix, tenant) come from
  the profile pair above — see [Profile / overrides](#profile--overrides).
- **Resource naming**: every newly created Azure resource (Foundry
  project/account, azd environment, agent, model deployment, resource group) is
  named with the `{prefix}-` value from your profile (and, in parallel-ready
  Tier 1 scenarios, a run-scoped `-{instance}` suffix) so test resources are easy to
  identify, keep distinct across scenarios and concurrent runs, and clean up. Note that some
  fields lowercase the value and replace invalid characters with hyphens — that
  normalization is expected (see `sanitizeAgentName` in the extension).
- `command:` invokes the installed extension as `azd ai agent …`.
- Init scenarios set `env: AZD_DISABLE_AGENT_DETECT: "1"` to disable agent
  auto-detection prompts.
- Every scenario asks the driver to screenshot key steps on a best-effort basis and file a
  finding (`report_finding`) for any confusing UX, error, or doc mismatch. Screenshot capture
  failures are recorded as observations and do not block the remaining scenario steps.

## Pre/post hooks

Scenarios use the tester's **`pre:`** and **`post:`** hook lists for host-side
setup and cleanup. Hooks run on the host (inside WSL on Windows), outside the
tmux session, **sequentially and fail-fast** unless a hook sets
`continue_on_error: true`. Each entry is a string or a mapping with `run`
(required), `cwd` (defaults to the scenario `cwd`, created if missing), `env`,
`continue_on_error` (default `false`), `timeout` (default **120s**), and `name`.
After a tester session starts, its worker always calls `finish_session` and then
attempts declared post hooks, even if a product goal fails. Hook failures are
reported separately and make the scenario fail without hiding the original
product finding.

How they're used here:

- **`pre` reset** — stateful Tier 0/1 scenarios `rm -rf` their own working dir so
  re-runs start clean. (`start_session` recreates the dir, so removing it is
  enough; the doctor/init scenarios just need an empty dir.)
- **`pre` fixture seed** — the existing-code scenarios
  (`1.04-init-from-code`, `1.06-init-deploy-mode-code`) also copy a committed Python
  fixture into the dir so the source exists before the wizard's "Use the code in
  the current directory" flow inspects it (see [Fixtures](#fixtures)).
- **`pre` gh-auth guard** — the manifest scenarios (`1.03-init-from-azure-yaml-url`,
  `1.05-init-flag-agent-name`) run `gh auth status` and fail fast if GitHub
  CLI isn't authenticated, because downloading the manifest can fall back to the
  `gh` CLI (and an interactive login) when the anonymous GitHub API is
  rate-limited. Run `gh auth login` first (see [Authentication](#authentication)).
- **`pre` idempotent setup (Tier 2)** — `2.00-setup-deploy-shared-agent` first runs
  `azd down --force --purge` if a project exists at the current run's
  `{shared_agent_name}` path, then clears only that project directory. A failed
  teardown aborts setup and preserves the project state for recovery. Other run
  directories under `~/working/azd-agents-shared` are left intact. The hook uses
  `timeout: 900`.
- **`pre` precondition guard (Tier 2 reuse)** — `2.01-`…`2.18` print a clear "run
  2.00-setup first" warning if the shared agent isn't deployed (non-fatal).
- **`post` cleanup (Tier 1b)** — every deploy-verification scenario runs
  `azd down --force --purge` with `timeout: 900` after its tester session,
  including when product verification fails.
- **Success-gated cleanup (Tier 2)** — `2.99-teardown-down` removes the current
  run's `{shared_agent_name}` project directory only after the in-session
  `azd down` succeeds. Failed teardown retains the directory for recovery.

## Fixtures

`fixtures/from-code/` holds a minimal runnable Agent Framework Responses server
(`app.py` + `requirements.txt`). It satisfies the extension's existing-code
detection (which looks for `requirements.txt` or any `.py` and defaults the
entry point to `app.py`) and remains responsive after deployment so Tier 1b can
verify `azd ai agent invoke`. The existing-code scenarios copy it into the
working dir via a `pre` hook, then select "Use the code in the current
directory" at the init prompt.

The hook references the fixture via the `{fixtures_dir}` session variable, which
the orchestrator auto-derives from the scenarios directory path:

```sh
cp -r "{fixtures_dir}/from-code/." "$cwd"
```

The orchestrator computes `fixtures_dir` as the tester-side absolute path of the
`fixtures/` subdirectory inside the scenarios directory (WSL-translated on Windows,
native on Linux/macOS) and passes it as a `session_var` alongside the other profile
variables.

## Re-running scenarios (idempotency)

Idempotency is handled **per scenario** via `pre`/`post` hooks rather than a
separate reset step — every scenario that holds state resets itself, so they can
be run back to back in any order within a tier:

- Tier 0/1 stateful scenarios **pre-wipe** their own `cwd`. Cleanup is pre-wipe
  **only** (no `post` delete), so the generated scaffold stays on disk for
  inspection after a run while the next run still starts clean.
- Tier 1b scenarios preserve the Tier 1 scaffold but use an always-run `post`
  hook to down their isolated Azure resources. Cleanup failure is a separate
  scenario failure and leaves the local environment available for recovery.
- The current Tier 2 run's `{shared_agent_name}` project dir is reset by
  `2.00-setup`'s `pre` hook, which **downs a deployed project at that exact path
  first** and aborts without deleting local state if teardown fails (this also
  sidesteps the resource-name hash collision behind
  [#8360](https://github.com/Azure/azure-dev/issues/8360)). Other run directories
  are preserved. After a successful teardown, `2.99-teardown-down` clears only
  the current run's project directory as its final interactive step.
- Read-only scenarios (`version`, `--help`, `sample list`) run in `/tmp`, hold no
  state, and declare no hooks.

> If a Tier 2 run is interrupted before `2.99-teardown`, run
> `2.99-teardown-down` with that run's original `shared_agent_name`. The cleanup
> deliberately does not remove directories belonging to other runs.

## Notes

- `files` and `sessions` are exercised as one lifecycle scenario per command
  group (rather than one file per subcommand) to avoid cross-scenario ordering
  dependencies — still one command at a time.
- `azd ai agent run` blocks the terminal; `2.12-run-local-and-invoke-local.yaml`
  uses two sessions (one to run, one to invoke `--local`) that share an
  allocated `{agent}` port (see
  [Parallel-readiness](#parallel-readiness--port-allocation)).
- Run artifacts (screenshots, HTML reports) land in `.reports/`, which is
  git-ignored.
