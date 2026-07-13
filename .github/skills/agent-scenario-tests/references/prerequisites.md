# Prerequisites

Verify these before doing anything else. If a hard prerequisite is missing, stop and tell
the user exactly what to fix — do **not** try to work around it.

### Repo location

1. Locate the scenarios directory:
   `cli/azd/extensions/azure.ai.agents/tests/cli-interactive-tester-scenarios/`.
   Resolve its absolute path; everything else is relative to it.
2. Note the **WSL path** of that directory for MCP tool arguments. On Windows hosts the
   tester runs inside WSL, so a Windows path like
   `C:\Repos\azure-dev\...\scenarios\00-version.yaml` must be passed as
   `/mnt/c/Repos/azure-dev/.../scenarios/00-version.yaml`. On macOS/Linux use the native
   absolute path. See `running-scenarios.md` § Path style.

### Tooling

| Requirement | Check | If missing |
| --- | --- | --- |
| `git` + `gh` CLIs | `gh auth status` | Ask the user to run `gh auth login`. |
| cli-interactive-tester MCP server | The `list_scenarios` / `start_session` MCP tools are available to you | Stop. Tell the user to register the cli-interactive-tester MCP server (see its README) and re-run. |
| `profile.local.yaml` | File exists in the scenarios dir | Stop. Tell the user to `cp profile.local.yaml.example profile.local.yaml` and set `prefix` + `subscription`. |
| Native Linux `azd` in WSL (Windows only) | `azd version` inside the tester returns a dev build, not a Windows `.exe` interop version | The skill automatically runs `setup-wsl.sh` (Step 1b) to rebuild from source. If you need to run it manually: `bash setup-wsl.sh` from the scenarios directory inside WSL. Symlinking to `azd.exe` does not work (causes git safe.directory, TTY, and file-locking errors). |

### Auth (tier-dependent — only enforce for tiers actually selected)

- **Tier 0** needs no auth.
- **Tier 1 / Tier 2** read from / write to Azure. A human must `az login` inside WSL
  **before** the run (the agent cannot complete the browser sign-in). If the selected set
  includes Tier 1/2, remind the user to `az login` first.
- **Manifest scenarios** (`10-init-from-manifest-url`, `10-init-flags-agent-name-model`)
  download from GitHub and can fall back to the `gh` CLI; they need `gh auth login` inside
  WSL. Their `pre` hook fails fast if it isn't set up.

### Profiles

The scenarios reference `{prefix}`, `{subscription}`, `{region}`, `{model}`, `{tenant}`
(optional) and `{shared_agent_name}` via placeholders. You must:

1. Read both `profile.yaml` (checked-in defaults) and `profile.local.yaml` (developer
   overrides) and **merge them, local overriding shared**.
2. Derive `shared_agent_name = "{prefix}-{shared_agent_suffix}"`.
3. Pass the merged map as `session_vars` on **every** `load_scenario`, `run_pre_hooks`,
   `start_session`, and `run_post_hooks` call. Omitting it leaves placeholders unresolved
   and the run executes against literal `{prefix}` strings.
