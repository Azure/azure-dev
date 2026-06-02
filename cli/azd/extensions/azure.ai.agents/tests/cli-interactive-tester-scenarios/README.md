<!-- cspell:ignore benhanrahan -->
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
  `~/working/azd-agents-*` dir per `init`/`doctor` scenario for isolation; and a
  single shared `~/working/azd-agents-shared` dir for all Tier 2 scenarios so they
  operate on the same deployed agent.

On macOS/Linux these are simply native paths (no WSL involved).

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
| `00-init-validate-mutually-exclusive.yaml` | `init` flag validation (`--from-code` + `-m`) |
| `00-init-validate-no-prompt-missing.yaml` | `init --no-prompt` missing-input error |
| `00-init-picker-navigation.yaml` | `init` interactive picker UX (abort before Azure) |

### Tier 1 — Auth, scaffold only (prefix `10-`)
Requires `azd auth login` (reads subscriptions/Foundry projects) but **does not
provision** any resources and incurs no cost. Each completes a project scaffold
and verifies the generated files, then stops before `azd provision`.

| File | Targets |
|------|---------|
| `10-init-template-python.yaml` | `init` new-from-template, Python |
| `10-init-template-dotnet.yaml` | `init` new-from-template, C#/.NET |
| `10-init-from-manifest-url.yaml` | `init -m <manifest url>` |
| `10-init-from-code.yaml` | `init --from-code` |
| `10-init-flags-agent-name-model.yaml` | `init -m … --agent-name --model` |
| `10-init-deploy-mode-code.yaml` | `init --deploy-mode code` (entry-point/runtime) |

### Tier 2 — Cloud end-to-end (prefix `2x-`) — ⚠️ incurs Azure cost
Provisions real resources. **Run order matters:**

1. `20-setup-deploy-shared-agent.yaml` **first** — deploys the shared agent.
2. Any `21-`…`2A-` targeted scenario (reuse the deployed agent).
3. `2Z-teardown-down.yaml` **last** — `azd down --force --purge`.

All Tier 2 scenarios share one working directory (`~/working/azd-agents-shared`)
so they operate on the same deployed agent.

| File | Targets |
|------|---------|
| `20-setup-deploy-shared-agent.yaml` | `init` + `azd provision` (SETUP) |
| `21-show.yaml` | `show` (table) |
| `21-show-json.yaml` | `show --output json` |
| `22-invoke-remote.yaml` | `invoke` (remote) |
| `22-invoke-new-session.yaml` | `invoke --new-session` |
| `22-invoke-input-file.yaml` | `invoke -f <file>` |
| `23-sessions-lifecycle.yaml` | `sessions create/list/show/delete` |
| `24-files-lifecycle.yaml` | `files upload/list/stat/mkdir/download/delete` |
| `25-monitor-console.yaml` | `monitor` (console) |
| `25-monitor-system.yaml` | `monitor --type system` |
| `26-endpoint-update.yaml` | `endpoint update` |
| `27-run-local-and-invoke-local.yaml` | `run` + `invoke --local` (two sessions) |
| `28-eval-init-run-show.yaml` | `eval init/run/list/show` |
| `28-eval-update.yaml` | `eval update` |
| `29-optimize-submit-status.yaml` | `optimize` + `optimize status/list` |
| `2A-doctor-provisioned-all-pass.yaml` | `doctor` (all checks pass) |
| `2Z-teardown-down.yaml` | `azd down --force --purge` (TEARDOWN) |

## Conventions

- **Subscription**: `azd ai agent development`
- **Region**: `East US 2`
- **Model**: `gpt-4.1-mini` (cheap/fast for testing)
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
- **`pre` fixture seed** — the `--from-code` scenarios
  (`10-init-from-code`, `10-init-deploy-mode-code`) also copy a committed Python
  fixture into the dir so the source exists before `init --from-code` inspects it
  (see [Fixtures](#fixtures)).
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
`requirements.txt`) that satisfies the extension's `--from-code` detection
(it looks for `requirements.txt` or any `.py`, and defaults the entry point to
`app.py`). The from-code scenarios copy it into the working dir via a `pre` hook.

The hook references the fixture by absolute path with an overridable env var:

```sh
cp -r "${AZD_AGENTS_FIXTURES:-/mnt/c/Repos/azure-dev/cli/azd/extensions/azure.ai.agents/tests/cli-interactive-tester-scenarios/fixtures}/from-code/." "$cwd"
```

If your clone lives somewhere other than `/mnt/c/Repos/azure-dev` (the WSL view
of `C:\Repos\azure-dev`), export `AZD_AGENTS_FIXTURES` to the WSL path of this
`fixtures/` directory before running the from-code scenarios.

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
  uses two sessions (one to run, one to invoke `--local`).
