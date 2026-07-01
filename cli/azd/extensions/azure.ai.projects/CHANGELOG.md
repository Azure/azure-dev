# Release History

## 1.0.0-beta.1 (2026-06-30)

### Features Added

- [[#8818]](https://github.com/Azure/azure-dev/pull/8818) The `azure.ai.projects` extension now registers the `azure.ai.project` service-target host so `azd deploy`/`azd up` can walk the Foundry project service in `azure.yaml`. The project and its model deployments are provisioned by the `microsoft.foundry` provider, so the deploy step is a no-op that owns the host.
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