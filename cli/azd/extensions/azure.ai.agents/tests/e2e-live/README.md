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

### Memory snapshot sample validation

This opt-in validates Python Responses code deployments and container images.
Both use the platform guide's SDK versions and snapshot lifecycle expectations;
only container mode performs its immutable-image qualification. It does not
cover .NET or every sample in the catalog. Ordinary and scheduled runs remain
unchanged with `memorySnapshot=false`.

Before running:

1. Confirm subscription approval and the approved region with the preview owner.
  Subscription approval does not establish regional availability. The previous
  `eastus2` run was rejected by the service gate; do not assume that region is
  approved merely because it remains the ordinary pipeline default.
2. Select a basic stateless Python Responses sample with `pyproject.toml` and
  `uv.lock`; container mode also requires a Dockerfile. The pinned Agent Framework Responses
  `01-basic` baseline is supported for automatic builds. CI updates only its
  isolated copy to the package versions below, retains the baseline hosting
  integration at `1.0.0b260821`, and regenerates the lock file and requirements
  with uv `0.11.7`, matching that sample's Dockerfile tooling.
  CI uses the repository's authenticated Azure Artifacts Python feed rather
  than direct PyPI access. In container mode, the isolated Dockerfile uses a digest-pinned MCR
  Python 3.13 Linux AMD64 base instead of direct GHCR or Docker Hub pulls.
  BuildKit supplies `UV_DEFAULT_INDEX` through a temporary secret mount for
  uv installation and dependency restore; feed tokens are not build arguments
  and are not persisted in image layers. Local automatic builds must set
  `UV_DEFAULT_INDEX` to the approved feed as well.
3. For `deployModes=code`, leave `snapshotImage` empty. The existing code ZIP
  deployment path skips customer ACR provisioning and does not build or inspect
  a Docker image. This is code-deploy validation, not the guide's container test.
  For `deployModes=container`, leave `snapshotImage` empty to build and push the
  sample into its provisioned ACR. CI builds Linux AMD64 and records the published digest before agent
  deployment. Alternatively, supply a prebuilt image using an immutable
  `registry/repository@sha256:<digest>` reference and matching sample source
  revision. Both the CI identity and the Foundry project must be able to pull
  the image. The test does not grant registry access or enable service flights.

| Runtime | Core package | Responses package |
| --- | --- | --- |
| Python 3.10+ (automated baseline) | `azure-ai-agentserver-core==2.3.0b1` | `azure-ai-agentserver-responses==2.4.0b1` |
| .NET 8 or .NET 10 (manual follow-up) | `Azure.AI.AgentServer.Core` `1.0.0-beta.30` | `Azure.AI.AgentServer.Responses` `1.0.0-beta.9` |

Basic stateless samples use the SDK's default lifecycle endpoints; custom
handlers are not required. Applications that own connections or other resources
that cannot safely be cloned may need cleanup and restore handlers.

Queue the live pipeline at the CI branch's commit with these explicit inputs:

| Parameter | Snapshot qualification value |
| --- | --- |
| `memorySnapshot` | `true` |
| `deployModes` | `code` or `container`; `both` is rejected for snapshot runs |
| `snapshotImage` | Empty for code; optional immutable image reference for container mode |
| `templateURL` | The provisioning manifest for that sample, pinned to a source revision |
| `subscriptionId` | Required explicit preview-approved subscription ID for snapshot runs |
| `serviceConnection` | Authentication identity with existing access to the target subscription |
| `location` | Confirmed preview-approved region |

The service connection's default subscription is not proof of preview approval.
The pipeline selects `subscriptionId`, verifies the active context before init,
and binds registry access and cleanup to that same subscription. An inaccessible
or mismatched target fails before resource creation; it never falls back to the
connection's default. Ordinary runs may omit `subscriptionId` to keep that default.

Code mode prepares the SDK dependencies before provisioning, preserves
`codeConfiguration`, and only adds `enableMemorySnapshot: true` to the service.
It reuses ordinary code deployment and verifies the active version's code
configuration and snapshot opt-in. It does not inspect the managed runtime's
installed packages, so source dependency pins are not runtime package evidence.

In container mode, existing-image checks run before creating a project. For automatic
builds, CI resolves dependencies and regenerates the lock file and requirements
before provisioning the sample project and registry. It then builds and publishes
the image and performs the same checks before creating the agent version.
The driver verifies Linux AMD64, Python 3.10+, and the exact Python SDK versions
using the image's actual Python startup command, including a `.venv` interpreter.
Dependency consistency is checked with `python -m pip check`, or `uv pip check`
for uv environments that do not include pip. Checks run with networking disabled.
Dependency checks keep the root filesystem read-only and mount only `/tmp` as
a 64 MiB, non-executable temporary memory filesystem for checker scratch files.
Docker authentication uses an isolated configuration cleaned up after the run.
Automatic builds use host Python 3.13 and pinned uv; they do not change existing
user projects.

Both modes set `enableMemorySnapshot: true`. Container mode additionally sets the qualified image
digest and image passthrough settings. Its image is the runtime under test.
An automatic-build result qualifies the explicitly dependency-updated sample,
not the unchanged catalog sample. A supplied-image result qualifies that
image/manifest/source-revision combination. Neither mode silently upgrades an
existing user project.

Agent creation still uses `azd deploy` and the existing `v1` REST mapping so the
azd opt-in is exercised. Success requires an active snapshot-enabled version
with code configuration in code mode or the exact qualified digest in container mode, followed by successful
Responses requests in two distinct sessions with explicit `store:false`. The
test uses authenticated `az rest` for those requests because the current
`azd ai agent invoke --input-file` Responses path treats file contents as message
text rather than accepting raw request fields. No new product CLI option is added.

The guide's direct REST creation payload is an optional troubleshooting control,
not a replacement for testing azd's deployment mapping. If application logs are
needed, connect Application Insights before agent creation and check for
`before-snapshot`. This bounded test does not prove idle replacement, filesystem
durability, or measured cold-start speedup. Keep subscription/region rejections
separate from SDK, image-access, activation, and invocation failures.

Logs for each run are published as the `tier2-live-logs-<BuildId>` artifact.

### One-time admin setup

1. **Register the pipeline** in Azure DevOps pointing at
   `eng/pipelines/ext-azure-ai-agents-live.yml`, named `ext-azure-ai-agents-live`
   (the name used by `/azp run`).
2. **Service connection** — the `serviceConnection` parameter (default
  `azure-sdk-tests`) authenticates via OIDC / workload-identity federation.
  Its identity must already have access to the selected `subscriptionId`;
  the parameter does not grant additional access. The identity needs enough RBAC to
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
| `E2E_TEMPLATE_URL`         | -                              | Explicit sample `azure.yaml` URL instead of the recommended picker entry |
| `E2E_MEMORY_SNAPSHOT`      | `false`                        | Enable Python container snapshot qualification; requires explicit container mode and preview approval |
| `E2E_SNAPSHOT_IMAGE`       | -                              | Optional image digest; empty builds the isolated Python sample; SDK versions and Linux AMD64 are checked |
| `E2E_SKIP_DEPLOY`          | —                              | `true` → run only init → provision → down (fast down/#8839 check) |
| `E2E_CREATE_PROJECT`       | `false`                        | `true` → always create a fresh Foundry project              |
| `E2E_PROJECT`              | —                              | Name of an existing Foundry project to select instead       |
| `E2E_LOCATION`             | `eastus2`                      | Region for new projects (needs model quota)                 |
| `E2E_SUBSCRIPTION`         | —                              | Subscription id (filters the picker)                        |
| `E2E_TENANT`               | —                              | AAD tenant id (sets `AZURE_TENANT_ID` for azd)              |
| `E2E_USE_AZ_CLI_AUTH`      | —                              | `true` → set `auth.useAzCliAuth` (CI; auto-on under ADO/GHA) |
| `E2E_TESTDIR`              | `/tmp/e2e-tests/tier2-<mode>`  | Scratch dir for the scaffolded project                      |
| `E2E_KEEP_ARTIFACTS`       | —                              | `true` → keep the per-run `AZD_CONFIG_DIR` copy for debugging |
| `AZURE_AI_AGENTS_E2E_TEMPLATE_CACHE_DIR` | —                 | CI cache for the last successfully downloaded sample         |
| `AZURE_AI_AGENTS_E2E_TEMPLATE_CACHE_WARNINGS_FILE` | —      | ADO-only per-job file for replaying successful cache fallback warnings outside the interactive PTY; keep outside the shared cache |
| `GH_TOKEN`                 | —                              | GitHub token for template clone (optional)                  |

In CI the driver auto-detects GitHub Actions (`GITHUB_ACTIONS`) and Azure DevOps
(`TF_BUILD`) and switches to `az` CLI auth automatically. The `down` phase tears
resources down interactively (answering the confirmation prompt), and a
`t.Cleanup` force teardown (`azd down --force --purge`) always runs on top, even
on failure.

The live pipeline clears the warning file before testing and replays it from an
always-run post-test step, even when no sample was refreshed. Only successful cache
fallback warnings are appended as escaped Azure Pipelines logging commands so both
deploy modes retain their diagnostics. The file is not part of the shared sample
artifact; reporting failures do not turn a successful cache fallback into an init
failure.

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
