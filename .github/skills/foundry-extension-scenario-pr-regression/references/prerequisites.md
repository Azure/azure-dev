# Prerequisites

Verify these before doing anything else. If a hard prerequisite is missing, stop and tell
the user exactly what to fix — do **not** try to work around it.

### Repo location

1. Locate the scenarios directory:
   `cli/azd/extensions/azure.ai.agents/tests/cli-interactive-tester-scenarios/`.
   Resolve its absolute path; everything else is relative to it.
2. Note the **WSL path** of that directory for MCP tool arguments. On Windows hosts the
   tester runs inside WSL, so a Windows path like
   `C:\Repos\azure-dev\...\scenarios\tier0\0.01-version.yaml` must be passed as
   `/mnt/c/Repos/azure-dev/.../scenarios/tier0/0.01-version.yaml`. On macOS/Linux use the native
   absolute path. See `driving-mechanics.md` § Path style (Windows → WSL).

### Tooling

| Requirement | Check | If missing |
| --- | --- | --- |
| `git` + `gh` CLIs | `gh auth status` | Ask the user to run `gh auth login`. |
| cli-interactive-tester MCP server | The `list_scenarios` / `start_session` MCP tools are available to you | Stop. Tell the user to register the cli-interactive-tester MCP server (see its README) and re-run. |
| `profile.local.yaml` | File exists in the scenarios dir | Stop. Tell the user to `cp profile.local.yaml.example profile.local.yaml` and set `prefix` + `subscription`. |
| WSL setup tools (Windows only) | Inside WSL, verify `git`, `curl` or `wget`, `awk`, `grep`, `tar`, `sha256sum`, `sha512sum`, `uname`, and `sudo` are available | **Hard stop.** `setup-wsl.sh` bootstraps the exact Go version from `cli/azd/go.mod` and .NET SDK from `dotnet-sdk.version`, but it does not install general OS packages. Tell the user which setup tool is missing. |
| Native Linux `azd` in WSL (Windows only) | After Step 1b, run `which azd` — must return `/usr/local/bin/azd` (not `/mnt/c/…` or a path ending in `azd.exe`). Then run `azd version` — must contain the expected dev version string. | **Hard stop.** If `which azd` returns a Windows interop path, file-locking on UNC paths will fail all init/provision scenarios. Re-run Step 1b (`setup-wsl.sh`), which also installs the pinned Go and .NET toolchains when needed. Do not proceed until both checks pass. |
| Native Linux `azd` (native Linux/macOS) | `azd version` returns the expected dev build | Ask the user to build and install `azd` from source. No special path check is needed — any valid `azd` path works on native Linux. |

### Auth (tier-dependent — only enforce for tiers actually selected)

- **Tier 0** needs no auth.
- **Tier 1 / Tier 2** read from / write to Azure. A human must `az login` inside WSL
  **before** the run (the agent cannot complete the browser sign-in). If the selected set
  includes Tier 1/2, remind the user to `az login` first.
- **Manifest scenarios** (`1.03-init-from-azure-yaml-url`, `1.05-init-flag-agent-name`)
  download from GitHub and can fall back to the `gh` CLI; they need `gh auth login` inside
  WSL. Their `pre` hook fails fast if it isn't set up.

### Profiles

The scenarios use `{prefix}`, `{subscription}`, `{region}`, `{model}`, and
`{shared_agent_name}` placeholders, plus optional `tenant`, per-scenario `instance`, and
Tier 1b `prerequisite_scaffold_dir` session values.
Tenant-aware goals deliberately avoid a literal `{tenant}` placeholder so a
missing optional value does not prevent a scenario from loading. You must:

1. Read both `profile.yaml` (checked-in defaults) and `profile.local.yaml` (developer
   overrides) and **merge them, local overriding shared**.
2. Generate one `run_id` for the whole sweep from a 10-digit
   month/day/hour/minute/second timestamp plus 6 lowercase hexadecimal characters (for
   example, `0714103842-a1b2c3`).
   Generate it once and reuse it across every scenario; seconds plus the random suffix
   isolate concurrent sweeps that start at nearly the same time.
3. Derive `shared_agent_name = "{prefix}-{shared_agent_suffix}-{run_id}"`. Tier 2
   scenarios use this value as both their Azure agent name and shared working-directory
   key. Retain it for recovery if a run is interrupted.
4. Derive `fixtures_dir` = the tester-side absolute path of the `fixtures/` subdirectory
   inside the scenarios directory. On Windows (where the tester runs inside WSL) this is
   the WSL-translated path (e.g. `/mnt/c/Repos/azure-dev/.../fixtures`); on native
   Linux/macOS it is the regular absolute path. Apply the same path-style logic used for
   scenario paths (see `driving-mechanics.md` § Path style (Windows → WSL)).
   Scenario pre-hooks use `{fixtures_dir}` to locate test fixture files.
5. Use `<run_id>` for the report directory and as the unique component of every worker
   `session_id`.
6. For each Tier 0 / Tier 1 parallel-safe scenario, derive a safe scenario key from its
   numeric stem (for example, `t101` for `1.01`) and set
   `instance = "<scenario-key>-<run_id>"`. Use only lowercase letters, digits, and
   hyphens. A Tier 1b scenario must reuse the exact `instance` assigned to its
   `requires:` prerequisite so it finds that scaffold. When running the same scenario
   multiple times, append a distinct ordinal to each copy.
7. Give each worker a per-scenario copy of the merged map. Include `run_id`,
   `shared_agent_name`, and `fixtures_dir`, plus `instance` when applicable, and pass
   that map unchanged as `session_vars` on **every** `load_scenario`, `run_pre_hooks`,
   `start_session`, and `run_post_hooks` call. Also pass the same value as `instance_id`
   to each hook/session tool that accepts it. Omitting or changing either value can
   render different paths at load and execution time.
8. A Tier 1 producer declares its output through the scenario YAML's top-level `produces`
   field. The worker renders that field with the producer's `session_vars`, verifies the
   scaffold, resolves it to an absolute path, and returns it as `scaffold_dir`. Before
   dispatching the declared Tier 1b dependent, add that exact path to its per-scenario map as
   `prerequisite_scaffold_dir`. Never reconstruct the path from `instance`, agent names, or
   template directory conventions.
