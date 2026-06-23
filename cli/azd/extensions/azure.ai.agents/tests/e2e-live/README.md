# azure.ai.agents — Live E2E (Tier 2)

Full golden-path tests that exercise the real `azd ai agent` CLI against **live
Azure** resources:

```
init → provision → deploy → invoke → down
```

A Python driver sends keystrokes to the CLI through a **tmux** session and asserts
on the captured output, for both deploy modes:

| Mode        | What it does                                            |
| ----------- | ------------------------------------------------------- |
| `code`      | Source-code (zip) deploy of the agent service           |
| `container` | Container (ACR build) deploy of the agent service       |

The two modes run **sequentially** (same subscription → avoids resource races).

## Where this fits

| Tier | Coverage                                  | Where it runs                                          |
| ---- | ----------------------------------------- | ------------------------------------------------------ |
| 0    | Offline CLI validation (no auth)          | PR gate — `.github/workflows/lint-ext-azure-ai-agents.yml` |
| 1    | `init` variants (recording/playback)      | PR gate — same workflow                                |
| 2    | **Full live golden path** (this folder)   | **`eng/pipelines/ext-azure-ai-agents-live.yml`**       |

Live Azure access is deliberately kept **out** of the automatic PR pipeline (Azure
SDK EngSys / SFI guidance). Tier 2 runs only on demand or on a schedule.

## Running in CI

Pipeline: `eng/pipelines/ext-azure-ai-agents-live.yml` (ADO).

- **On demand (per PR):** comment `/azp run ext-azure-ai-agents-live` on the PR.
  Requires write permission on the repo.
- **Scheduled:** weekly, Monday 07:00 UTC against `main`.
- **Manual:** queue the pipeline and pick `deployModes` = `both` / `code` /
  `container`.

Logs for each run are published as the `tier2-live-logs-<BuildId>` artifact.

### One-time admin setup

1. **Register the pipeline** in Azure DevOps pointing at
   `eng/pipelines/ext-azure-ai-agents-live.yml`, named `ext-azure-ai-agents-live`
   (the name used by `/azp run`).
2. **Service connection** — the `serviceConnection` parameter (default
   `azure-sdk-tests`) must map to the shared **TME test subscription** via OIDC /
   workload-identity federation. The federated identity needs enough RBAC to
   create Foundry projects and deploy models (Contributor + Azure AI Developer +
   Cognitive Services Contributor, or equivalent).
3. **GitHub auth** — clones of the starter template use the azure-sdk org secret
   `azuresdk-github-pat` (already provided by the Azure SDK ADO project) to avoid
   anonymous rate limits, so no extra secret setup is required.

## Running locally (Linux / WSL)

Prerequisites: Linux (including WSL) with `tmux` (>= 3.2), Python 3.12+, `azd`
(>= 1.25.5) with the `azure.ai.agents` extension installed, and `az` logged in.

```bash
# Use azd's built-in auth locally (NOT az CLI auth — it is slow under WSL).
azd config unset auth.useAzCliAuth
azd auth login

# Both modes (sequential):
python3 test_tier2.py --mode both

# A single golden path:
python3 test_full_e2e.py --deploy-mode code
python3 test_full_e2e.py --deploy-mode container --keep   # leave resources up
```

### Useful environment variables

| Variable               | Default      | Purpose                                                        |
| ---------------------- | ------------ | -------------------------------------------------------------- |
| `E2E_CREATE_PROJECT`   | `false`      | `true` → always create a fresh Foundry project                 |
| `E2E_LOCATION`         | `eastus2`    | Region for new projects (needs model quota)                    |
| `E2E_SUBSCRIPTION`     | —            | Subscription id (filters the picker)                           |
| `E2E_TENANT`           | —            | AAD tenant id                                                  |
| `E2E_USE_AZ_CLI_AUTH`  | —            | `true` → set `auth.useAzCliAuth` (CI; auto-on under ADO/GHA)   |
| `GH_TOKEN`             | —            | GitHub token for template clone (optional)                     |

In CI the driver auto-detects GitHub Actions (`GITHUB_ACTIONS`) and Azure DevOps
(`TF_BUILD`) and switches to `az` CLI auth automatically.

## Files

| File               | Purpose                                                            |
| ------------------ | ----------------------------------------------------------------- |
| `test_tier2.py`    | Runner — invokes `test_full_e2e.py` once per deploy mode          |
| `test_full_e2e.py` | One golden path: setup → init → provision → deploy → invoke → down |

Each phase has bounded timeouts and best-effort `azd down --force --purge`
teardown so a crash mid-run does not leak billable resources.
