# Release History

## 0.4.1-preview (Unreleased)

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