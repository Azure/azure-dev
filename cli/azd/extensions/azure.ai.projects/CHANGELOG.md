# Release History

## 1.0.0-beta.7 (2026-08-24)

### Other Changes

- [[#9580]](https://github.com/Azure/azure-dev/pull/9580) Update the azd extension SDK dependency to v1.31.0 for extension telemetry compatibility.

## 1.0.0-beta.6 (2026-08-13)

### Features Added

- [[#9457]](https://github.com/Azure/azure-dev/pull/9457) Support Foundry provisioning as an isolated infrastructure layer with layer paths, virtual outputs, ownership tracking, repeat provisioning, and teardown behavior.

## 1.0.0-beta.5 (2026-08-06)

### Features Added

- [[#9079]](https://github.com/Azure/azure-dev/pull/9079) Add service-scoped environment support to Foundry project provisioning while preserving raw templates and project-wide fallback behavior.

### Bugs Fixed

- [[#9326]](https://github.com/Azure/azure-dev/pull/9326) Publish Foundry dependency readiness state so agent deployments can fail early with actionable guidance when resources are not ready.
- [[#9367]](https://github.com/Azure/azure-dev/pull/9367) Fix Foundry project synthesis so network environment references use shared defaults, escaping, and unresolved-variable validation.

## 1.0.0-beta.4 (2026-07-30)

### Bugs Fixed

- [[#9292]](https://github.com/Azure/azure-dev/pull/9292) Fix Foundry ARM deployment names exceeding ARM's 64-character limit when long azd environment names are used. Long environment-name segments are now truncated while retaining deterministic environment and project-path hashes for uniqueness.

## 1.0.0-beta.3 (2026-07-23)

### Features Added

- [[#9133]](https://github.com/Azure/azure-dev/pull/9133) The `azure.ai.projects` extension now owns Foundry project provisioning through the `microsoft.foundry` provider, including updating deployments and connections on existing projects (set `AZURE_AI_PROJECT_ID` to the project ARM resource ID), using the customer VNet region for private endpoints, and blocking automatic Azure Container Registry creation for private-network projects. Release it together with `azure.ai.agents`, since mixing versions can cause both extensions to register the same provider.

### Bugs Fixed

- [[#9149]](https://github.com/Azure/azure-dev/pull/9149) Fix Foundry project synthesis and provisioning not consistently resolving configuration declared inline in `azure.yaml`, via the deprecated `config:` block, or through local `$ref` files.

## 1.0.0-beta.2 (2026-07-09)

### Other Changes

- [[#9027]](https://github.com/Azure/azure-dev/pull/9027) Bump `golang.org/x/crypto` to v0.53.0 (and transitively `golang.org/x/net` to v0.55.0) to address security advisories.

## 1.0.0-beta.1 (2026-06-30)

### Features Added

- [[#8818]](https://github.com/Azure/azure-dev/pull/8818) The `azure.ai.projects` extension now registers the `azure.ai.project` service-target host so `azd deploy`/`azd up` can walk the Foundry project service in `azure.yaml`. The project and its model deployments are provisioned by the built-in `microsoft.foundry` Bicep provider, so the deploy step is a no-op that owns the host.
- [[#8890]](https://github.com/Azure/azure-dev/pull/8890) Bump `requiredAzdVersion` to `>=1.27.0`.
- [[#8651]](https://github.com/Azure/azure-dev/pull/8651) Update Go to 1.26.4 and bump golang.org/x/crypto and golang.org/x/net. Thanks @hemarina for the contribution!

## 0.1.0-preview (2026-05-28)

Initial preview release of the Foundry Projects extension.

### Features Added

- Added `azd ai project set <endpoint>` to persist a default Foundry project endpoint to the azd global config (`~/.azd/config.json`). Other AI extensions resolve this endpoint when no azd environment variable or explicit flag is available.
- Added `azd ai project show` to display the currently resolved Foundry project endpoint and the source that provided it, for easy debugging.
- Added `azd ai project unset` to clear the persisted Foundry project endpoint from global config (idempotent — safe to run when no endpoint is set).
- Endpoint resolution uses a 5-level cascade: explicit `--project-endpoint` flag → active azd environment's `FOUNDRY_PROJECT_ENDPOINT` → global config (`extensions.ai-projects.context.endpoint`) → host `FOUNDRY_PROJECT_ENDPOINT` environment variable → actionable structured error.
- One-time auto-migration from the legacy `extensions.ai-agents.project.context` key (written by the removed `azd ai agent project set` command) into the new `extensions.ai-projects.context` key.
- All commands support `--output table` (default) and `--output json` for machine-readable output.