# azure.ai.agents — Live E2E (Tier 2)

Full golden-path tests that exercise the real `azd ai agent` CLI against **live
Azure** resources:

```
init → provision → deploy → invoke → down
```

A Go test driver answers the interactive `azd ai agent init` prompts through a
**pseudo-terminal** — [go-expect] sends keystrokes and [vt10x] renders the CLI's
terminal UI so the test can assert on the on-screen text, with [creack/pty]
providing the PTY. Synchronization is **event-driven**: the driver blocks on
go-expect reads until the survey UI stops emitting — i.e. a prompt is fully
drawn and waiting for input — instead of sleeping a fixed interval, then
dispatches on the rendered prompt text. The deploy mode is chosen up front via
`azd ai agent init --deploy-mode code|container` (it is not an interactive
prompt once a manifest is supplied). The `provision`, `deploy`, and `invoke`
phases shell out to `azd ... --no-prompt`. The `down` phase deliberately runs
**interactively without `--force`** (over the same PTS harness) to exercise the
Foundry provider's destroy confirmation prompt (issue #8839): the driver waits
for the "Are you sure you want to continue?" prompt, verifies it names the
resource group, answers "yes", and then asserts (via `az group exists`) that the
group was actually deleted. A `t.Cleanup` still runs `azd down --force --purge
--no-prompt` as an idempotent safety net so a crash mid-run never leaks
resources. Both deploy modes are covered:

| Mode        | What it does                                            |
| ----------- | ------------------------------------------------------- |
| `code`      | Source-code (zip) deploy of the agent service           |
| `container` | Container (ACR build) deploy of the agent service       |

The two modes run **sequentially** (same subscription → avoids resource races).

[go-expect]: https://github.com/Netflix/go-expect
[vt10x]: https://github.com/hinshun/vt10x
[creack/pty]: https://github.com/creack/pty

## How the `init` driver answers prompts

The interactive sub-flows (Foundry project selection, model/deployment) branch
on live runtime state, so the exact set and order of prompts is not fixed ahead
of time. Rather than a linear expect script, the driver runs a **dispatch
loop**: it waits for output to settle, reads the rendered screen, matches the
active `?` prompt against the verbatim strings the extension prints — each case
in `dispatchPrompt` is annotated with the source `file:line` it mirrors — and
sends the answer. A loop detector bounds any prompt that fails to advance so a
wording change upstream fails fast instead of hanging.

Because the prompt strings are calibrated against the extension source, changes
there can require updating `dispatchPrompt`. And because a real PTY, Azure auth,
and the installed extension are all required, the **end-to-end interactive
correctness is only exercised by a live Tier 2 run** — it cannot be reproduced
by the platform-agnostic unit tests in this package.

## Where this fits

| Tier | Coverage                                  | Where it runs                                          |
| ---- | ----------------------------------------- | ------------------------------------------------------ |
| 0    | Offline CLI validation (no auth)          | PR gate — `.github/workflows/lint-ext-azure-ai-agents.yml` |
| 1    | `init` variants (recording/playback)      | PR gate — same workflow                                |
| 2    | **Full live golden path** (this folder)   | **`eng/pipelines/ext-azure-ai-agents-live.yml`**       |

Live Azure access is deliberately kept **out** of the automatic PR pipeline (Azure
SDK EngSys / SFI guidance). Tier 2 runs only on demand or on a schedule.

## Running in CI

Pipeline: `eng/pipelines/ext-azure-ai-agents-live.yml` (ADO). The Tier 2 step
builds `azd` + the extension and runs `go test -run TestTier2Live` inside an
`AzureCLI@2` task (so the federated az session stays valid for the whole run).

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

The live driver is tagged `//go:build linux` — it relies on a real PTY and a
controlling terminal (the platform CI runs on). On Windows, run it under WSL.

Prerequisites: Linux (including WSL), a Go toolchain matching `go.mod`
(`GOTOOLCHAIN=auto` fetches the right version automatically), `azd` (>= 1.25.5)
with the `azure.ai.agents` extension installed, and `az` logged in.

Run from the extension root (`cli/azd/extensions/azure.ai.agents`):

```bash
# Use azd's built-in auth locally (NOT az CLI auth — it is slow under WSL).
azd config unset auth.useAzCliAuth
azd auth login

# Both modes (sequential):
AZURE_AI_AGENTS_E2E_LIVE=1 E2E_DEPLOY_MODES=both \
  go test -run TestTier2Live -count=1 -timeout 130m -v ./tests/e2e-live/

# A single golden path:
AZURE_AI_AGENTS_E2E_LIVE=1 E2E_DEPLOY_MODES=code \
  go test -run TestTier2Live -count=1 -timeout 90m -v ./tests/e2e-live/
```

Without `AZURE_AI_AGENTS_E2E_LIVE=1` the test is **skipped**, so the package is
safe to include in a normal `go test ./...`.

### Useful environment variables

| Variable                   | Default                        | Purpose                                                      |
| -------------------------- | ------------------------------ | ----------------------------------------------------------- |
| `AZURE_AI_AGENTS_E2E_LIVE` | —                              | **Required** `=1` gate; unset → the test is skipped         |
| `E2E_DEPLOY_MODES`         | `both`                         | `both` / `code` / `container`                               |
| `E2E_SKIP_DEPLOY`          | —                              | `true` → run only init → provision → down (fast down/#8839 check) |
| `E2E_CREATE_PROJECT`       | `false`                        | `true` → always create a fresh Foundry project              |
| `E2E_PROJECT`              | —                              | Name of an existing Foundry project to select instead       |
| `E2E_LOCATION`             | `eastus2`                      | Region for new projects (needs model quota)                 |
| `E2E_SUBSCRIPTION`         | —                              | Subscription id (filters the picker)                        |
| `E2E_TENANT`               | —                              | AAD tenant id (sets `AZURE_TENANT_ID` for azd)              |
| `E2E_USE_AZ_CLI_AUTH`      | —                              | `true` → set `auth.useAzCliAuth` (CI; auto-on under ADO/GHA) |
| `E2E_TESTDIR`              | `/tmp/e2e-tests/tier2-<mode>`  | Scratch dir for the scaffolded project                      |
| `E2E_KEEP_ARTIFACTS`       | —                              | `true` → keep the per-run `AZD_CONFIG_DIR` copy for debugging |
| `GH_TOKEN`                 | —                              | GitHub token for template clone (optional)                  |

In CI the driver auto-detects GitHub Actions (`GITHUB_ACTIONS`) and Azure DevOps
(`TF_BUILD`) and switches to `az` CLI auth automatically. The `down` phase tears
resources down interactively (answering the confirmation prompt), and a
`t.Cleanup` force teardown (`azd down --force --purge`) always runs on top, even
on failure.

## Files

| File                 | Purpose                                                                          |
| -------------------- | -------------------------------------------------------------------------------- |
| `tier2_live_test.go` | `TestTier2Live` — drives init/provision/deploy/invoke/down per mode (Linux-only) |
| `console_test.go`    | PTY + vt10x console helper that renders the interactive CLI (Linux-only)         |
| `assert.go`          | Pure-logic answer matcher (`responseHasExpectedAnswer`) — builds on any platform |
| `assert_test.go`     | Unit tests for the matcher — run anywhere via `go test ./tests/e2e-live/`        |

Each phase has bounded timeouts. The `down` phase runs an interactive `azd down`
(exercising the destroy confirmation prompt), and a best-effort `azd down --force
--purge` `t.Cleanup` teardown runs on top so a crash mid-run does not leak
billable resources.
