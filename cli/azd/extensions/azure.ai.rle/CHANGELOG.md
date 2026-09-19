# Release History

## 0.8.8-preview

- `azd ai rle rollout`'s underlying HTTP client timeout is now 300s (was
  30s). The 30s wall was masking real, longer-running RLE processing (a
  full Harness/BYOH rollout can take 60-90s+) as a blind
  "context deadline exceeded" with no server-side detail; 300s gives real
  rollouts room to return their actual result or a specific RLE error.

## 0.8.7-preview

- `azd ai rle rollout` now forwards the resolved Foundry project endpoint on
  the executeRollout request (`model.project_endpoint`), matching Vienna's
  per-rollout Loom Capture Proxy routing. No new flag or env var is required:
  it reuses the same `FOUNDRY_PROJECT_ENDPOINT`/`--project-endpoint` value
  already resolved to build the RLE client for this call.

## 0.8.6-preview

- Block RLE lifecycle commands when the development registry marks a newer release
  as breaking, while leaving metadata and version commands available for updates and
  diagnostics.
- Show an update notice after normal command output when a newer non-breaking
  RLE extension is available.
- Rename `azd ai rle invoke` to `azd ai rle rollout` (no behavior change).

## 0.8.5-preview

- Version bump only; no functional changes since 0.8.4-preview.

## 0.8.4-preview

- `azd ai rle invoke`'s `--model` now falls back to rle.toml's `defaults.model.name` when omitted, so ad hoc rollouts against a source folder that already declares a model default only need `--task` (and, for Harness targets, `--agent-input`).

## 0.8.3-preview

- Align environment discovery and remote invocation with the refreshed RLE service routes and cursor-based response contracts.
- Use `/rl_environments` consistently for environment and instance lifecycle APIs.
- Manage remote invoke through temporary instance groups and instances instead of direct sandbox lifecycle APIs.
- Retry runtime creation while a published environment's disk image is still being prepared.
- Replace `.azd-rle.json` with a host-agnostic `rle.toml` manifest as the sole local RLE identity.
- Define RLE releases with control-plane `type` and `subtype` values: `Gym: OpenEnv`, `Harness: HostedAgent`, and `Harness: BYOH`.
- Pin local invoke to the manifest's `(name, version)` identity and require `--version` for source-free invocation.
- Derive an explicit service version bump from the manifest version and verify the published release identity.
- Tag published ACR images with the manifest RLE version rather than `latest`.
- Delete the temporary instance and group on exit with Ctrl+C-independent cleanup and concise terminal status.
- Authenticate and API-version OpenEnv gateway requests on the configured Foundry project origin, wait for runtime health before reporting readiness, and route the browser playground through an authenticated local proxy.
- Initialize a required local folder by interactively selecting and sparsely downloading an environment from the RLE samples repository, including its manifest.
- Filter Gym/OpenEnv samples by the samples repository's visibility catalog, and gate harness init targets, hidden samples, and other internal-only surfaces behind a single `AZD_AI_RLE_ENABLE_ALL` flag.
- Add `azd ai rle train` (experimental, gated behind `AZD_AI_RLE_ENABLE_ALL`) to submit an RLE-backed reinforcement fine-tuning job via finetunesapi's `rl_environment` method, using a published RLE environment as the reward source instead of a grader.
- Repurpose `azd ai rle invoke` from an interactive OpenEnv playground shell into a rollout executor: it now provisions a real Loom training session and sampler checkpoint, calls RLE's Execute Rollout API with a `--model` (required), `--task`/`--task-file`, and (for Harness targets) `--agent-input`/`--agent-input-file`, prints the reward/success/episode summary, and tears down the Loom session — so callers never handle Loom session or checkpoint identifiers directly.

## 0.3.0-preview

- Add `azd ai rle list` to list environments in the configured Foundry project.
- Add `azd ai rle show <environment-name>` to inspect an environment's full details and version history.
- Allow `azd ai rle invoke <environment-name>` to invoke an existing project environment without local source or state, with optional `--version` selection.
- Rename `azd ai rle deploy` to `azd ai rle publish` to avoid confusion with the core `azd deploy` command.
- Add `--version-bump` to `azd ai rle publish` so users can choose major, minor, or patch environment versioning.
- Use the Foundry project endpoint for project-relative RLE environment and sandbox APIs.
- Authenticate Foundry API requests with Azure credentials from `az login`, `azd auth login`, or another supported development credential.
- Send the required `2025-11-15-preview` Foundry data-plane API version.
- Support versioned environment deployments and sandbox `baseUrl` invocation.
- Wait for asynchronous disk-image conversion before leasing a sandbox and surface conversion failures directly.

## 0.1.0-preview

- Initial preview scaffold for the RLE extension with `init`, `run`, `invoke`, `deploy`, and `version` commands.